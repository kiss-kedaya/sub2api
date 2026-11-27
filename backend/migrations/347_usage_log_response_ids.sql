-- 仅触碰 usage_logs 一张热表；可空无默认值列为纯元数据变更，限定 5s 锁等待超时。
SET LOCAL lock_timeout = '5s';

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS response_request_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS client_request_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS response_msg_id VARCHAR(128);

COMMENT ON COLUMN usage_logs.response_request_id IS
    'X-Request-Id response header returned to the client for request tracing';
COMMENT ON COLUMN usage_logs.client_request_id IS
    'X-Client-Request-Id response header returned to the client for request tracing';
COMMENT ON COLUMN usage_logs.response_msg_id IS
    'Claude/Anthropic response message id (e.g. msg_...) when group save_response_msg_id is enabled';
