package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration344SupportTicketAttachments(t *testing.T) {
	raw, err := FS.ReadFile("344_support_ticket_attachments.sql")
	require.NoError(t, err)

	statements := strings.Join(strings.Fields(regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(raw), "")), " ")
	lower := strings.ToLower(statements)

	require.Contains(t, statements, "SET LOCAL lock_timeout = '5s'")
	require.Contains(t, statements, "CREATE TABLE IF NOT EXISTS support_ticket_attachments (")
	require.Contains(t, statements, "ticket_id BIGINT REFERENCES support_tickets(id) ON DELETE CASCADE")
	require.Contains(t, statements, "message_id BIGINT REFERENCES support_ticket_messages(id) ON DELETE CASCADE")
	require.Contains(t, statements, "uploader_id BIGINT NOT NULL REFERENCES users(id)")
	require.Contains(t, statements, "content_type IN ('image/png', 'image/jpeg', 'image/webp', 'image/gif')")
	require.Contains(t, statements, "size_bytes INTEGER NOT NULL CHECK (size_bytes BETWEEN 1 AND 5242880)")
	require.Contains(t, statements, "data BYTEA NOT NULL")
	require.NotContains(t, lower, "image/svg")

	lockAt := strings.Index(statements, "SET LOCAL lock_timeout = '5s'")
	createAt := strings.Index(statements, "CREATE TABLE")
	require.NotEqual(t, -1, lockAt)
	require.Less(t, lockAt, createAt)

	for _, forbidden := range []string{"alter table", "drop ", "truncate", "delete from", "update ", "concurrent"} {
		require.NotContains(t, lower, forbidden)
	}
}

func TestMigration344IsOrderedAfter343AndOwnsItsNumber(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	var with344 []string
	index343, index344 := -1, -1
	for i, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "344_") {
			with344 = append(with344, name)
		}
		switch name {
		case "343_user_security_preferences.sql":
			index343 = i
		case "344_support_ticket_attachments.sql":
			index344 = i
		}
	}
	require.Equal(t, []string{"344_support_ticket_attachments.sql"}, with344)
	require.NotEqual(t, -1, index343)
	require.NotEqual(t, -1, index344)
	require.Less(t, index343, index344)
}
