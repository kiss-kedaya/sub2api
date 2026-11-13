-- 保留期清理删除保留窗口之外的老行后，会用最老被删行的日期回退发布水位。
-- 该日期恒等于 retained_from 的日期，于是水位被永久钉在保留期起点，下一轮
-- SyncGroupUsageRollups 就以它为起点重建——等于每个周期重算整个保留窗口。
-- 已关闭日的原始数据不再变化，这轮重建完全白做。
--
-- 这里给两个失效触发器加上同样的守卫：受影响日期落在保留窗口之外时不回退水位。
-- 窗口内的真实数据变更（补数、重算、时区切换）仍然照旧回退。

CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_date DATE;
    published_before DATE;
    retained_before DATE;
BEGIN
    IF TG_OP = 'DELETE' THEN
        affected_date := (OLD.created_at AT TIME ZONE 'Asia/Shanghai')::date;
    ELSE
        IF OLD.group_id IS NULL THEN
            affected_date := (NEW.created_at AT TIME ZONE 'Asia/Shanghai')::date;
        ELSIF NEW.group_id IS NULL THEN
            affected_date := (OLD.created_at AT TIME ZONE 'Asia/Shanghai')::date;
        ELSE
            affected_date := LEAST(
                (OLD.created_at AT TIME ZONE 'Asia/Shanghai')::date,
                (NEW.created_at AT TIME ZONE 'Asia/Shanghai')::date
            );
        END IF;
    END IF;

    IF affected_date IS NULL THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;

    -- 即使当前已发布水位尚未越过受影响日期，也必须先锁行。
    -- 否则并发关闭作业可能在本事务之后把水位推进，覆盖本次失效。
    SELECT closed_before, retained_from::date
    INTO published_before, retained_before
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR UPDATE;

    -- 受影响日期已在汇总窗口之外（保留期清理删掉的就是这类行），
    -- 回退只会换来一次整窗重建，不会修正任何仍然保留的日桶。
    IF published_before > affected_date AND affected_date > retained_before THEN
        UPDATE usage_group_rollup_state
        SET closed_before = LEAST(closed_before, affected_date),
            updated_at = NOW()
        WHERE id = 1;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_date DATE;
    published_before DATE;
    retained_before DATE;
BEGIN
    SELECT MIN((created_at AT TIME ZONE 'Asia/Shanghai')::date)
    INTO affected_date
    FROM inserted_usage_logs
    WHERE group_id IS NOT NULL;

    IF affected_date IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT closed_before, retained_from::date
    INTO published_before, retained_before
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR KEY SHARE;

    IF published_before > affected_date AND affected_date > retained_before THEN
        UPDATE usage_group_rollup_state
        SET closed_before = LEAST(closed_before, affected_date),
            updated_at = NOW()
        WHERE id = 1;
    END IF;

    RETURN NULL;
END;
$$;
