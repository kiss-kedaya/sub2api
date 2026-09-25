package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestTicketEmailConversationIncludesAllPagesWithoutReadSideEffects(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	ticketExpectLookup(mock, ticketSQLRows("waiting_user", 90), false, true)
	ids := make([]int64, 60)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	mock.ExpectQuery(`WHERE m.ticket_id = \$1 AND m.id <= \$2 ORDER BY m.id ASC`).WithArgs(int64(10), int64(60)).WillReturnRows(ticketMessageRows(ids...)).RowsWillBeClosed()
	detail, err := repo.EmailConversation(context.Background(), 10, 60)
	require.NoError(t, err)
	require.Len(t, detail.Messages, 60)
	require.Equal(t, int64(1), detail.Messages[0].ID)
	require.Equal(t, int64(60), detail.Messages[59].ID)
	require.Nil(t, detail.Requester)
}

func TestTicketEmailConversationRejectsInvalidReplyBoundary(t *testing.T) {
	for _, test := range []struct {
		name, role, kind string
		id               int64
	}{
		{"not exact boundary", "admin", "reply", 59},
		{"user reply", "user", "reply", 60},
		{"status event", "admin", "event", 60},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, mock := ticketSQLMock(t)
			ticketExpectLookup(mock, ticketSQLRows("waiting_user", 90), false, true)
			rows := sqlmock.NewRows([]string{"id", "ticket_id", "author_id", "author_role", "author_name", "content", "kind", "event_type", "event_data", "created_at"}).AddRow(test.id, 10, 9, test.role, "support", "reply", test.kind, "", []byte("{}"), ticketSQLTime)
			mock.ExpectQuery(`WHERE m.ticket_id = \$1 AND m.id <= \$2 ORDER BY m.id ASC`).WithArgs(int64(10), int64(60)).WillReturnRows(rows)
			_, err := repo.EmailConversation(context.Background(), 10, 60)
			require.ErrorIs(t, err, service.ErrTicketNotFound)
		})
	}
}

func TestTicketEmailConversationPropagatesReadFailure(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	ticketExpectLookup(mock, ticketSQLRows("waiting_user", 60), false, true)
	mock.ExpectQuery(`WHERE m.ticket_id = \$1 AND m.id <= \$2 ORDER BY m.id ASC`).WithArgs(int64(10), int64(60)).WillReturnError(errors.New("database unavailable"))
	_, err := repo.EmailConversation(context.Background(), 10, 60)
	require.ErrorContains(t, err, "database unavailable")
}
