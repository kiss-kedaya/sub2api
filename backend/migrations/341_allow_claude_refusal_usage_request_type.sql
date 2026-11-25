-- Claude 违规拒答（上游 stop_reason=refusal）的用量行记为 request_type=6，
-- 用量页「类型」列显示「Claude违规拒答」，该行不计费。
-- 不放宽约束的话，这类用量行写库会被 usage_logs_request_type_check 拒绝（整条用量丢失）。
-- NOT VALID：usage_logs 是大表，不做全表校验；存量行本来就都在 0..5 内，只约束新写入的行。
ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_type_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_type_check
    CHECK (request_type >= 0 AND request_type <= 6) NOT VALID;
