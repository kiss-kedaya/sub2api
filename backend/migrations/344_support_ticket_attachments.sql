SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS support_ticket_attachments (
    id BIGSERIAL PRIMARY KEY,
    ticket_id BIGINT REFERENCES support_tickets(id) ON DELETE CASCADE,
    message_id BIGINT REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
    uploader_id BIGINT NOT NULL REFERENCES users(id),
    file_name TEXT NOT NULL DEFAULT '' CHECK (char_length(file_name) <= 255),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp', 'image/gif')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880),
    width INTEGER NOT NULL CHECK (width BETWEEN 1 AND 8192),
    height INTEGER NOT NULL CHECK (height BETWEEN 1 AND 8192),
    sha256 TEXT NOT NULL CHECK (char_length(sha256) = 64),
    data BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK ((ticket_id IS NULL AND message_id IS NULL) OR (ticket_id IS NOT NULL AND message_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS support_ticket_attachments_message_idx
    ON support_ticket_attachments (message_id, id)
    WHERE message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS support_ticket_attachments_ticket_idx
    ON support_ticket_attachments (ticket_id, id)
    WHERE ticket_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS support_ticket_attachments_orphan_idx
    ON support_ticket_attachments (created_at)
    WHERE message_id IS NULL;

CREATE INDEX IF NOT EXISTS support_ticket_attachments_uploader_orphan_idx
    ON support_ticket_attachments (uploader_id, created_at)
    WHERE message_id IS NULL;
