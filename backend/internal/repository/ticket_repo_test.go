package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var ticketSQLTime = time.Date(2026, 9, 24, 15, 59, 59, 0, time.UTC)
var ticketSQLActor = service.TicketActor{UserID: 7}

const ticketSQLUUID = "6b7a9b04-1952-491c-ae6d-a2da990d8a8b"

func ticketSQLMock(t *testing.T) (*ticketRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	mock.MatchExpectationsInOrder(true)
	return &ticketRepository{db: db}, mock
}

func ticketSQLRows(status string, last int64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "user_id", "subject", "contact", "category", "priority", "status", "assignee_id", "assignee_name", "user_name", "user_email", "created_at", "updated_at", "last_message_at", "last_message_preview", "unread_count", "last_message_id"}).
		AddRow(10, 7, "help", "contact", "api", "normal", status, nil, "", "", "", ticketSQLTime, ticketSQLTime, ticketSQLTime, "reply", 2, last)
}

func ticketMessageRows(ids ...int64) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"id", "ticket_id", "author_id", "author_role", "author_name", "content", "kind", "event_type", "event_data", "created_at"})
	for _, id := range ids {
		rows.AddRow(id, 10, 9, "admin", "support", "reply", "reply", "", []byte("{}"), ticketSQLTime)
	}
	return rows
}

func ticketExpectOwner(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO support_ticket_user_locks").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT user_id FROM support_ticket_user_locks WHERE user_id = \\$1 FOR UPDATE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(7))
}

func ticketExpectLookup(mock sqlmock.Sqlmock, rows *sqlmock.Rows, lock bool, admin bool) {
	pattern := `SELECT t.id, t.user_id, t.subject, t.contact,.*FROM support_tickets t.*WHERE t.id = \$1 AND `
	if admin {
		pattern += "TRUE"
	} else {
		pattern += `t.user_id = \$2`
	}
	if lock {
		pattern += " FOR UPDATE OF t"
	}
	expect := mock.ExpectQuery(pattern)
	if admin {
		expect.WithArgs(int64(10))
	} else {
		expect.WithArgs(int64(10), int64(7))
	}
	expect.WillReturnRows(rows)
}

func TestTicketRepositoryOwnerIsolation(t *testing.T) {
	for _, action := range []string{"detail", "reply", "update", "read"} {
		t.Run(action, func(t *testing.T) {
			r, mock := ticketSQLMock(t)
			if action != "detail" {
				mock.ExpectBegin()
			}
			pattern := `SELECT t.id,.*WHERE t.id = \$1 AND t.user_id = \$2`
			if action == "read" {
				pattern = `SELECT t.id FROM support_tickets t WHERE t.id = \$1 AND t.user_id = \$2 FOR UPDATE`
			}
			mock.ExpectQuery(pattern).WithArgs(int64(10), int64(7)).WillReturnError(sql.ErrNoRows)
			if action != "detail" {
				mock.ExpectRollback()
			}
			var err error
			switch action {
			case "detail":
				_, err = r.Detail(context.Background(), ticketSQLActor, 10, 0)
			case "read":
				err = r.MarkRead(context.Background(), ticketSQLActor, 10, 100)
			default:
				err = r.Mutate(context.Background(), ticketSQLActor, 10, ticketSQLUUID, "hash", func(*service.Ticket) (*service.TicketChange, error) {
					t.Fatal("unauthorized callback")
					return nil, nil
				})
			}
			require.ErrorIs(t, err, service.ErrTicketNotFound)
		})
	}
}

func TestTicketRepositoryMessagePaginationAndDeliveryWatermark(t *testing.T) {
	for _, before := range []int64{0, 71} {
		t.Run(string(rune('a'+before)), func(t *testing.T) {
			r, mock := ticketSQLMock(t)
			ticketExpectLookup(mock, ticketSQLRows("open", 120), false, false)
			if before != 0 {
				mock.ExpectQuery("SELECT EXISTS .*support_ticket_messages").WithArgs(int64(10), before, int64(120)).WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(true))
			}
			latest := int64(120)
			if before > 0 {
				latest = before - 1
			}
			ids := make([]int64, 51)
			for i := range ids {
				ids[i] = latest - int64(i)
			}
			mock.ExpectQuery(`WHERE m.ticket_id = \$1 AND m.id <= \$2 AND .*ORDER BY m.id DESC LIMIT 51`).
				WithArgs(int64(10), int64(120), before).WillReturnRows(ticketMessageRows(ids...)).RowsWillBeClosed()
			mock.ExpectExec("INSERT INTO support_ticket_views .*GREATEST").WithArgs(int64(10), int64(7), false, latest).WillReturnResult(sqlmock.NewResult(0, 1))
			detail, err := r.Detail(context.Background(), ticketSQLActor, 10, before)
			require.NoError(t, err)
			require.Len(t, detail.Messages, 50)
			require.True(t, detail.HasMore)
			require.Equal(t, latest-49, detail.Messages[0].ID)
			require.Equal(t, latest, detail.Messages[49].ID)
			require.Nil(t, detail.Requester)
			require.Equal(t, int64(120), detail.Ticket.LastMessageID)
		})
	}
}

func TestTicketRepositoryRejectsForeignPageCursor(t *testing.T) {
	r, mock := ticketSQLMock(t)
	ticketExpectLookup(mock, ticketSQLRows("open", 120), false, false)
	mock.ExpectQuery("SELECT EXISTS .*support_ticket_messages").WithArgs(int64(10), int64(300), int64(120)).WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(false))
	_, err := r.Detail(context.Background(), ticketSQLActor, 10, 300)
	require.Error(t, err)
}

func TestTicketRepositoryReadOnlyAcknowledgesDeliveredID(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, seen := range []bool{false, true} {
			t.Run(service.TicketActor{Admin: admin}.Role()+"/"+map[bool]string{true: "delivered", false: "future or foreign"}[seen], func(t *testing.T) {
				r, mock := ticketSQLMock(t)
				actor := ticketSQLActor
				actor.Admin = admin
				mock.ExpectBegin()
				query := `SELECT t.id FROM support_tickets t WHERE t.id = \$1 AND `
				if admin {
					mock.ExpectQuery(query + "TRUE FOR UPDATE").WithArgs(int64(10)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
				} else {
					mock.ExpectQuery(query+`t.user_id = \$2 FOR UPDATE`).WithArgs(int64(10), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
				}
				mock.ExpectQuery(`SELECT EXISTS .*m.id = \$4.*v.last_seen_id >= \$4`).WithArgs(int64(10), int64(7), admin, int64(100)).WillReturnRows(sqlmock.NewRows([]string{"seen"}).AddRow(seen))
				if seen {
					column := "user_read_id"
					if admin {
						column = "admin_read_id"
					}
					mock.ExpectExec(regexp.QuoteMeta("UPDATE support_tickets SET "+column+" = GREATEST("+column+", $2) WHERE id = $1")).WithArgs(int64(10), int64(100)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				} else {
					mock.ExpectRollback()
				}
				err := r.MarkRead(context.Background(), actor, 10, 100)
				if seen {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, service.ErrTicketReadCursor)
				}
			})
		}
	}
}

func TestTicketRepositoryCreateIdempotencyPrecedesDayAndRateLimits(t *testing.T) {
	for _, hash := range []string{"original", "different"} {
		t.Run(hash, func(t *testing.T) {
			r, mock := ticketSQLMock(t)
			ticketExpectOwner(mock)
			// The original ticket may have been created yesterday and closed. No day
			// filter or quota operation is allowed before returning the original ID.
			mock.ExpectQuery(regexp.QuoteMeta("SELECT id, request_hash FROM support_tickets WHERE user_id = $1 AND client_id = $2")).WithArgs(int64(7), ticketSQLUUID).WillReturnRows(sqlmock.NewRows([]string{"id", "request_hash"}).AddRow(10, "original"))
			if hash == "original" {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			id, err := r.Create(context.Background(), ticketSQLActor, service.CreateTicketInput{ClientID: ticketSQLUUID}, hash)
			if hash == "original" {
				require.NoError(t, err)
				require.Equal(t, int64(10), id)
			} else {
				require.ErrorIs(t, err, service.ErrTicketClientConflict)
			}
		})
	}
}

func ticketExpectNewCreate(mock sqlmock.Sqlmock, used bool) {
	ticketExpectOwner(mock)
	mock.ExpectQuery("SELECT id, request_hash FROM support_tickets").WithArgs(int64(7), ticketSQLUUID).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT EXISTS .*creation_day = \(clock_timestamp\(\) AT TIME ZONE 'Asia/Shanghai'\)::date`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(used))
}

func TestTicketRepositoryDailyLimitIncludesClosedTickets(t *testing.T) {
	r, mock := ticketSQLMock(t)
	ticketExpectNewCreate(mock, true)
	mock.ExpectRollback()
	_, err := r.Create(context.Background(), ticketSQLActor, service.CreateTicketInput{ClientID: ticketSQLUUID}, "hash")
	require.ErrorIs(t, err, service.ErrTicketDailyLimit)
}

func TestTicketRepositoryCreateAtomicAndUniqueConstraint(t *testing.T) {
	for _, failure := range []string{"", "unique", "message", "rate"} {
		t.Run(failure, func(t *testing.T) {
			r, mock := ticketSQLMock(t)
			ticketExpectNewCreate(mock, false)
			rate := mock.ExpectQuery("INSERT INTO support_ticket_rate_limits.*RETURNING count").WithArgs(int64(7), "create", 3)
			if failure == "rate" {
				rate.WillReturnError(sql.ErrNoRows)
			} else {
				rate.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			}
			if failure != "rate" {
				insert := mock.ExpectQuery(`INSERT INTO support_tickets \(user_id, subject, contact, category, priority, client_id, request_hash\)`).WithArgs(int64(7), "help", "contact", "api", "normal", ticketSQLUUID, "hash")
				if failure == "unique" {
					insert.WillReturnError(&pq.Error{Code: "23505", Constraint: "support_tickets_user_day_unique"})
				} else {
					insert.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
					message := mock.ExpectQuery("INSERT INTO support_ticket_messages").WithArgs(int64(10), int64(7), "user", "body", "reply", "", "{}", "", "")
					if failure == "message" {
						message.WillReturnError(errors.New("disk full"))
					} else {
						message.WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(20, ticketSQLTime))
					}
				}
			}
			if failure == "" {
				mock.ExpectExec("UPDATE support_tickets SET status").WithArgs(int64(10), "open", "normal", nil, int64(20), "body", ticketSQLTime).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			_, err := r.Create(context.Background(), ticketSQLActor, service.CreateTicketInput{Subject: "help", Contact: "contact", Content: "body", Category: "api", Priority: "normal", ClientID: ticketSQLUUID}, "hash")
			switch failure {
			case "":
				require.NoError(t, err)
			case "unique":
				require.ErrorIs(t, err, service.ErrTicketDailyLimit)
			case "rate":
				require.ErrorIs(t, err, service.ErrTicketRateLimit)
			default:
				require.EqualError(t, err, "disk full")
			}
		})
	}
}

func TestTicketRepositoryReplyReplayDoesNotOverwriteClosedState(t *testing.T) {
	r, mock := ticketSQLMock(t)
	mock.ExpectBegin()
	ticketExpectLookup(mock, ticketSQLRows("closed", 100), true, false)
	mock.ExpectQuery("SELECT request_hash FROM support_ticket_messages").WithArgs(int64(10), int64(7), "user", ticketSQLUUID).WillReturnRows(sqlmock.NewRows([]string{"request_hash"}).AddRow("hash"))
	mock.ExpectCommit()
	err := r.Mutate(context.Background(), ticketSQLActor, 10, ticketSQLUUID, "hash", func(*service.Ticket) (*service.TicketChange, error) {
		t.Fatal("replay must not recompute state")
		return nil, nil
	})
	require.NoError(t, err)
}

func TestTicketRepositoryReplyRollsBackStatusAndEvent(t *testing.T) {
	r, mock := ticketSQLMock(t)
	mock.ExpectBegin()
	ticketExpectLookup(mock, ticketSQLRows("resolved", 100), true, false)
	mock.ExpectQuery("SELECT request_hash FROM support_ticket_messages").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("INSERT INTO support_ticket_rate_limits").WithArgs(int64(7), "write", 20).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("INSERT INTO support_ticket_messages").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(101, ticketSQLTime))
	mock.ExpectQuery("INSERT INTO support_ticket_messages").WillReturnError(errors.New("reply failed"))
	mock.ExpectRollback()
	err := r.Mutate(context.Background(), ticketSQLActor, 10, ticketSQLUUID, "hash", func(ticket *service.Ticket) (*service.TicketChange, error) {
		ticket.Status = "open"
		return &service.TicketChange{Ticket: ticket, Messages: []service.TicketMessage{{Kind: "event", Content: "reopened", AuthorRole: "user"}, {Kind: "reply", Content: "body", AuthorRole: "user"}}}, nil
	})
	require.EqualError(t, err, "reply failed")
}

func TestTicketRepositoryStatsUsesShanghaiDayAndUTCReset(t *testing.T) {
	for _, canCreate := range []bool{false, true} {
		r, mock := ticketSQLMock(t)
		mock.ExpectQuery(`SELECT COUNT\(\*\),.*m.author_role = 'admin'.*FROM support_tickets t WHERE t.user_id = \$1`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"total", "open", "progress", "waiting", "resolved", "closed", "unread"}).AddRow(2, 0, 0, 0, 1, 1, 1))
		midnight := time.Date(2026, 9, 25, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
		mock.ExpectQuery(`SELECT NOT EXISTS .*creation_day = \(statement_timestamp\(\) AT TIME ZONE 'Asia/Shanghai'\)::date`).WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"can_create", "next"}).AddRow(canCreate, midnight))
		stats, err := r.Stats(context.Background(), ticketSQLActor)
		require.NoError(t, err)
		require.Equal(t, canCreate, stats.CanCreate)
		if canCreate {
			require.Nil(t, stats.NextCreateAt)
		} else {
			require.Equal(t, "2026-09-24T16:00:00Z", stats.NextCreateAt.Format(time.RFC3339))
		}
	}
}

func TestTicketRepositoryListScopedBatchedAndNoEmail(t *testing.T) {
	r, mock := ticketSQLMock(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM support_tickets t WHERE t.user_id = \$1 AND t.status = \$2 AND t.subject ILIKE \$3`).WithArgs(int64(7), "open", `%literal\%%`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT t.id,.*'', '',.*ORDER BY t.updated_at DESC, t.id DESC LIMIT \$4 OFFSET \$5`).WithArgs(int64(7), "open", `%literal\%%`, 20, 20).WillReturnRows(ticketSQLRows("open", 20)).RowsWillBeClosed()
	items, total, err := r.List(context.Background(), ticketSQLActor, service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: 2, PageSize: 20}, Status: "open", Search: "literal%"})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	require.Empty(t, items[0].UserEmail)
}

func TestTicketRepositoryActiveListPreservesScopeAndPagination(t *testing.T) {
	for _, admin := range []bool{false, true} {
		t.Run(service.TicketActor{Admin: admin}.Role(), func(t *testing.T) {
			r, mock := ticketSQLMock(t)
			actor := service.TicketActor{UserID: 7, Admin: admin}
			scope := `t.user_id = \$1`
			args := []any{int64(7)}
			if admin {
				scope = "TRUE"
				args = nil
			}
			count := mock.ExpectQuery(`SELECT COUNT\(\*\).*WHERE ` + scope + ` AND t.status <> 'closed'`)
			list := mock.ExpectQuery(`SELECT t.id,.*WHERE ` + scope + ` AND t.status <> 'closed'.*ORDER BY t.updated_at DESC, t.id DESC LIMIT .* OFFSET`)
			if len(args) == 0 {
				count.WithArgs()
				list.WithArgs(20, 20)
			} else {
				count.WithArgs(int64(7))
				list.WithArgs(int64(7), 20, 20)
			}
			count.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			list.WillReturnRows(ticketSQLRows("resolved", 20)).RowsWillBeClosed()
			items, total, err := r.List(context.Background(), actor, service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: 2, PageSize: 20}, Status: "active"})
			require.NoError(t, err)
			require.Equal(t, int64(1), total)
			require.Len(t, items, 1)
			require.Equal(t, "resolved", items[0].Status)
		})
	}
}

func TestTicketRepositoryAdminDetailRefreshesRequester(t *testing.T) {
	r, mock := ticketSQLMock(t)
	actor := service.TicketActor{UserID: 9, Admin: true}
	for _, balance := range []float64{12.5, 9.25} {
		ticketExpectLookup(mock, ticketSQLRows("open", 100), false, true)
		mock.ExpectQuery("SELECT m.id, m.ticket_id").WillReturnRows(ticketMessageRows(100)).RowsWillBeClosed()
		mock.ExpectQuery("SELECT id, COALESCE\\(username, ''\\), email, balance, status, created_at FROM users WHERE id = \\$1").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "balance", "status", "created_at"}).AddRow(7, "requester", "requester@example.test", balance, "active", ticketSQLTime))
		mock.ExpectExec("INSERT INTO support_ticket_views").WithArgs(int64(10), int64(9), true, int64(100)).WillReturnResult(sqlmock.NewResult(0, 1))
		detail, err := r.Detail(context.Background(), actor, 10, 0)
		require.NoError(t, err)
		require.Equal(t, balance, detail.Requester.Balance)
		require.Equal(t, "requester", detail.Requester.Username)
	}
}
