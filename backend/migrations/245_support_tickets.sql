CREATE TABLE IF NOT EXISTS support_tickets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    subject TEXT NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 160),
    contact TEXT NOT NULL DEFAULT '' CHECK (char_length(contact) <= 200),
    category TEXT NOT NULL CHECK (category IN ('billing', 'api', 'account', 'other')),
    priority TEXT NOT NULL CHECK (priority IN ('normal', 'high', 'urgent')),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'waiting_user', 'resolved', 'closed')),
    assignee_id BIGINT REFERENCES users(id),
    client_id UUID NOT NULL,
    creation_day DATE NOT NULL DEFAULT ((clock_timestamp() AT TIME ZONE 'Asia/Shanghai')::date),
    request_hash TEXT NOT NULL,
    user_read_id BIGINT NOT NULL DEFAULT 0,
    admin_read_id BIGINT NOT NULL DEFAULT 0,
    last_message_id BIGINT NOT NULL DEFAULT 0,
    last_message_preview TEXT NOT NULL DEFAULT '',
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (user_id, client_id),
    CONSTRAINT support_tickets_user_day_unique UNIQUE (user_id, creation_day)
);

CREATE TABLE IF NOT EXISTS support_ticket_messages (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    author_id BIGINT REFERENCES users(id),
    author_role TEXT NOT NULL CHECK (author_role IN ('user', 'admin', 'system')),
    content TEXT NOT NULL CHECK (char_length(content) BETWEEN 1 AND 10000),
    kind TEXT NOT NULL CHECK (kind IN ('reply', 'event')),
    event_type TEXT NOT NULL DEFAULT '',
    event_data JSONB NOT NULL DEFAULT '{}',
    client_id UUID,
    request_hash TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (jsonb_typeof(event_data) = 'object')
);

-- Serialize creation without locking billing/user rows; retries check client_id first.
CREATE TABLE IF NOT EXISTS support_ticket_user_locks (
    user_id BIGINT PRIMARY KEY REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS support_ticket_rate_limits (
    user_id BIGINT NOT NULL REFERENCES users(id),
    action TEXT NOT NULL CHECK (action IN ('create', 'write')),
    window_start TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    count INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (user_id, action)
);

-- Delivery watermarks are distinct from read cursors. GET never marks messages read.
CREATE TABLE IF NOT EXISTS support_ticket_views (
    ticket_id BIGINT NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
    viewer_id BIGINT NOT NULL REFERENCES users(id),
    admin_view BOOLEAN NOT NULL,
    last_seen_id BIGINT NOT NULL,
    PRIMARY KEY (ticket_id, viewer_id, admin_view)
);

CREATE INDEX IF NOT EXISTS support_tickets_user_updated_idx ON support_tickets (user_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_updated_idx ON support_tickets (updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_status_updated_idx ON support_tickets (status, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_assignee_updated_idx ON support_tickets (assignee_id, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS support_ticket_messages_page_idx ON support_ticket_messages (ticket_id, id DESC);
CREATE INDEX IF NOT EXISTS support_ticket_messages_unread_idx ON support_ticket_messages (ticket_id, author_role, id) WHERE kind = 'reply';
CREATE UNIQUE INDEX IF NOT EXISTS support_ticket_messages_client_idx ON support_ticket_messages (ticket_id, author_id, author_role, client_id) WHERE client_id IS NOT NULL;
