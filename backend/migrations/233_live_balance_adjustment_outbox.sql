-- Durable delivery of committed, non-usage users.balance deltas to the
-- persistent Redis live wallet. The trigger makes enqueueing part of the same
-- PostgreSQL transaction as the balance mutation, closing the commit-to-Redis
-- crash window.

CREATE TABLE IF NOT EXISTS live_balance_adjustment_heads (
    user_id BIGINT PRIMARY KEY CHECK (user_id > 0),
    last_event_id BIGINT NOT NULL DEFAULT 0 CHECK (last_event_id >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE billing_balance_settlements
    ADD COLUMN IF NOT EXISTS wallet_preapplied BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS live_balance_adjustment_outbox (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL CHECK (user_id > 0),
    predecessor_id BIGINT NOT NULL DEFAULT 0 CHECK (predecessor_id >= 0),
    delta NUMERIC(20, 8) NOT NULL CHECK (delta <> 0 AND delta <> 'NaN'::numeric),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claimed_at TIMESTAMPTZ,
    claimed_by TEXT,
    delivered_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_live_balance_adjustment_outbox_pending
    ON live_balance_adjustment_outbox (available_at, id)
    WHERE delivered_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_live_balance_adjustment_outbox_lease
    ON live_balance_adjustment_outbox (claimed_at, id)
    WHERE delivered_at IS NULL AND claimed_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_live_balance_adjustment_outbox_user_order
    ON live_balance_adjustment_outbox (user_id, id)
    WHERE delivered_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_live_balance_adjustment_outbox_delivered
    ON live_balance_adjustment_outbox (delivered_at, id)
    WHERE delivered_at IS NOT NULL;

ALTER TABLE live_balance_adjustment_outbox
    SET (fillfactor = 90,
         autovacuum_vacuum_scale_factor = 0.02,
         autovacuum_vacuum_threshold = 1000,
         autovacuum_analyze_scale_factor = 0.01,
         autovacuum_analyze_threshold = 1000);

CREATE OR REPLACE FUNCTION enqueue_live_balance_adjustment()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    skip_outbox TEXT;
    balance_delta NUMERIC(20, 8);
    predecessor BIGINT;
    event_id BIGINT;
BEGIN
    IF OLD.balance IS NOT DISTINCT FROM NEW.balance THEN
        RETURN NEW;
    END IF;

    -- Usage reservations already mutate the live wallet before PostgreSQL is
    -- settled. Their transactions set this local marker to avoid a second
    -- Redis debit when the database catches up.
    skip_outbox := current_setting('sub2api.skip_live_balance_outbox', TRUE);
    IF COALESCE(NULLIF(skip_outbox, ''), 'off') = 'on' THEN
        RETURN NEW;
    END IF;

    balance_delta := NEW.balance - OLD.balance;
    IF balance_delta = 0 THEN
        RETURN NEW;
    END IF;

    INSERT INTO live_balance_adjustment_heads (user_id)
    VALUES (NEW.id)
    ON CONFLICT (user_id) DO NOTHING;

    SELECT last_event_id
    INTO predecessor
    FROM live_balance_adjustment_heads
    WHERE user_id = NEW.id
    FOR UPDATE;

    INSERT INTO live_balance_adjustment_outbox (user_id, predecessor_id, delta)
    VALUES (NEW.id, predecessor, balance_delta)
    RETURNING id INTO event_id;

    UPDATE live_balance_adjustment_heads
    SET last_event_id = event_id,
        updated_at = NOW()
    WHERE user_id = NEW.id;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_users_live_balance_adjustment ON users;
CREATE TRIGGER trg_users_live_balance_adjustment
AFTER UPDATE OF balance ON users
FOR EACH ROW EXECUTE FUNCTION enqueue_live_balance_adjustment();

COMMENT ON TABLE live_balance_adjustment_outbox IS
    'Durable at-least-once delivery of non-usage users.balance deltas to the Redis live wallet';
COMMENT ON TABLE live_balance_adjustment_heads IS
    'Persistent per-user outbox watermark; retained after delivered event cleanup and user deletion';
COMMENT ON COLUMN live_balance_adjustment_outbox.predecessor_id IS
    'Previous event for this user; Redis applies only when its wallet watermark matches this value';
COMMENT ON COLUMN live_balance_adjustment_outbox.user_id IS
    'Intentionally not a foreign key: delivery remains recoverable when a user is soft/hard deleted in parallel';
COMMENT ON COLUMN live_balance_adjustment_outbox.delivered_at IS
    'Redis accepted the stable outbox event id; delivered rows are retained briefly for operations visibility';
COMMENT ON COLUMN billing_balance_settlements.wallet_preapplied IS
    'True only after this charge was finalized in the Redis live wallet; PostgreSQL catch-up then skips generic wallet outbox delivery';
