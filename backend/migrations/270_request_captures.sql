-- Request capture bodies live in DATA_DIR/request-captures, never in PostgreSQL.
CREATE TABLE IF NOT EXISTS request_capture_tasks (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    data JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS request_capture_tasks_instance_created ON request_capture_tasks(instance_id, created_at DESC);
CREATE TABLE IF NOT EXISTS request_capture_records (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES request_capture_tasks(id) ON DELETE CASCADE,
    request_id TEXT NOT NULL DEFAULT '',
    is_error BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    data JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS request_capture_records_task_created ON request_capture_records(task_id, created_at DESC);
CREATE INDEX IF NOT EXISTS request_capture_records_request ON request_capture_records(request_id);
