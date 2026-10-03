-- ops_error_logs：补三份请求/响应头快照，用于出错时定位「客户端发了什么头」
-- 「我们发给上游什么头」「上游回了什么头」。
--
-- 入站快照对每个错误请求都写；出站与回程快照只在上游调用真正发生过时才写。
-- 全部为 JSONB 且可空，与 033 迁移里被 136 删掉的 request_headers 不同：
--   033 那份是为「重放/重试」存的完整请求头（含凭据），随重放功能一起删除；
--   本份是脱敏后的诊断快照，凭据类头（authorization / x-api-key / cookie 等）
--   在写入前就被丢弃，条数与长度均有上限（见 internal/service/ops_header_snapshot.go）。
ALTER TABLE ops_error_logs
    ADD COLUMN IF NOT EXISTS request_headers JSONB,
    ADD COLUMN IF NOT EXISTS upstream_request_headers JSONB,
    ADD COLUMN IF NOT EXISTS upstream_response_headers JSONB;

COMMENT ON COLUMN ops_error_logs.request_headers IS
    'Sanitized inbound request headers (client -> gateway). Credential headers are dropped, values truncated.';
COMMENT ON COLUMN ops_error_logs.upstream_request_headers IS
    'Sanitized outbound request headers (gateway -> upstream), captured on the final upstream attempt.';
COMMENT ON COLUMN ops_error_logs.upstream_response_headers IS
    'Sanitized upstream response headers (upstream -> gateway), captured on the final upstream attempt.';
