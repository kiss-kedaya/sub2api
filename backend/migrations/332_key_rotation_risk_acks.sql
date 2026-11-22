-- 密钥轮换风险确认（risk acknowledgement）
--
-- 背景：密钥泄漏事件后，门禁要求用户要么轮换全部启用中的 API Key，要么给每个
-- Key 设置 IP 白名单。部分用户两件事都不愿意做，但仍需要充值/兑换。这里记录
-- 用户「已知晓风险、自愿继续充值」的显式确认：
--
--   * 一次确认只在一段有限时间内有效（security.key_rotation_risk_ack_hours，默认 24h）；
--   * 只放行充值 / 兑换 / USDT 充值，模型调用本来就不受门禁影响；
--   * cutoff_snapshot 记录确认当时的 gate cutoff：运维调整 cutoff（新一轮事件）后
--     旧确认自动失效，用户必须重新确认；
--   * client_ip / user_agent 一并落库，配合审计日志（KEY_ROTATION_RISK_ACK）
--     让「谁在什么时候、从哪个 IP、确认了什么风险」可追溯。
--
-- 幂等：可重复执行（IF NOT EXISTS）。单表 append-only，不修改既有数据。

CREATE TABLE IF NOT EXISTS key_rotation_risk_acks (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    client_ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    cutoff_snapshot TIMESTAMPTZ NOT NULL,   -- 当时 gate cutoff，cutoff 变化后 ack 失效
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 校验路径按 user_id 查最近一条有效期内的确认，并只取 expires_at/cutoff_snapshot。
CREATE INDEX IF NOT EXISTS idx_key_rotation_risk_acks_user
    ON key_rotation_risk_acks (user_id, expires_at DESC);
