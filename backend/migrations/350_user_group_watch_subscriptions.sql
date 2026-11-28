-- 分组状态与改价订阅通知（Channel Monitor V3 卡片铃铛订阅）：
--   1. user_group_watch_subscriptions：记录用户订阅的分组（按 group_id 建索引支撑变更扇出）；
--   2. group_monitor_watch_states：持久化各被订阅分组最近一次有效监控状态（black/green/other）及
--      black_notified 标记，确保跨进程重启与多实例蓝绿切换不漏发、不重复发「变黑不可用 / 恢复绿色」；
--   3. user_group_watch_notifications：持久化站内通知（供顶部铃铛展示与已读状态），并以
--      UNIQUE(user_id, dedupe_key) 保证站内 + Bark + 邮件三通道至多投递一次。
--
-- 锁安全：本迁移纯增量新建三张表，不改任何既有表结构；外键引用 users/groups 时显式设置
-- SET LOCAL lock_timeout = '5s' 防止长时间等锁，回滚无害。
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS user_group_watch_subscriptions (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_user_group_watch_subs_group_id
    ON user_group_watch_subscriptions (group_id);

CREATE TABLE IF NOT EXISTS group_monitor_watch_states (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    status VARCHAR(16) NOT NULL,
    black_notified BOOLEAN NOT NULL DEFAULT FALSE,
    last_bucket_start TIMESTAMPTZ NOT NULL,
    cache_rate DOUBLE PRECISION NOT NULL DEFAULT 0,
    availability_pct DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_group_watch_notifications (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    event_type VARCHAR(32) NOT NULL,
    dedupe_key VARCHAR(160) NOT NULL,
    title VARCHAR(200) NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    delivered_channels JSONB NOT NULL DEFAULT '{}'::jsonb,
    delivery_attempts SMALLINT NOT NULL DEFAULT 1,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_user_group_watch_notifs_user_dedupe UNIQUE (user_id, dedupe_key)
);

CREATE INDEX IF NOT EXISTS idx_user_group_watch_notifs_user_created
    ON user_group_watch_notifications (user_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_user_group_watch_notifs_user_unread
    ON user_group_watch_notifications (user_id)
    WHERE read_at IS NULL;
