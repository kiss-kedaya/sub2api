package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Run with TICKET_TEST_DATABASE_URL set to an explicitly supplied local test DB.
// Only an isolated generated schema is changed, and only migration 245 is applied.
func ticketPostgres(t *testing.T) (*sql.DB, *service.TicketService, context.Context) {
	t.Helper()
	raw := os.Getenv("TICKET_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TICKET_TEST_DATABASE_URL is not set")
	}
	u, err := url.Parse(raw)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, u.Scheme)
	require.Contains(t, []string{"localhost", "127.0.0.1", "::1"}, u.Hostname(), "ticket integration tests require a local database")
	root, err := sql.Open("postgres", raw)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	require.NoError(t, root.PingContext(ctx))
	schema := "ticket_test_" + uuid.New().String()[:8]
	_, err = root.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, err := root.ExecContext(cleanup, "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	})
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(20)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `CREATE TABLE users (
		id BIGINT PRIMARY KEY, username TEXT, email TEXT NOT NULL, balance NUMERIC(20,8) NOT NULL DEFAULT 100,
		status TEXT NOT NULL DEFAULT 'active', role TEXT NOT NULL DEFAULT 'user', deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp());
		CREATE TABLE settings (id BIGSERIAL PRIMARY KEY, key VARCHAR(100) UNIQUE NOT NULL, value TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		INSERT INTO users (id,username,email) SELECT n, 'user' || n, 'user' || n || '@example.test' FROM generate_series(1,30) n;
		INSERT INTO users (id,username,email,role) VALUES (99,'support99','support99@example.test','admin'), (100,'support100','support100@example.test','admin');`)
	require.NoError(t, err)
	ddl, err := migrations.FS.ReadFile("245_support_tickets.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.ExecContext(ctx, string(ddl))
		require.NoError(t, err)
	}
	repo := NewTicketRepository(db)
	return db, service.NewTicketService(repo, &ticketPostgresUsers{db: db}), ctx
}

type ticketPostgresUsers struct {
	service.UserRepository
	db *sql.DB
}

func (r *ticketPostgresUsers) GetByID(ctx context.Context, id int64) (*service.User, error) {
	u := &service.User{}
	err := r.db.QueryRowContext(ctx, "SELECT id, username, email, balance, status, role, created_at, deleted_at FROM users WHERE id = $1", id).
		Scan(&u.ID, &u.Username, &u.Email, &u.Balance, &u.Status, &u.Role, &u.CreatedAt, &u.DeletedAt)
	return u, err
}

func ticketPGInput() service.CreateTicketInput {
	return service.CreateTicketInput{Subject: "API request failure", Content: "Please check this request", Contact: "contact-handle", Category: "api", Priority: "normal", ClientID: uuid.NewString()}
}

func ticketPGCreate(t *testing.T, s *service.TicketService, ctx context.Context, userID int64) *service.TicketDetail {
	t.Helper()
	d, err := s.Create(ctx, service.TicketActor{UserID: userID}, ticketPGInput())
	require.NoError(t, err)
	return d
}

func TestTicketPostgresConcurrentDailyCreation(t *testing.T) {
	db, s, ctx := ticketPostgres(t)
	for _, sameClient := range []bool{true, false} {
		owner := int64(1)
		if !sameClient {
			owner = 2
		}
		in := ticketPGInput()
		type outcome struct {
			id  int64
			err error
		}
		results := make(chan outcome, 16)
		start := make(chan struct{})
		for range 16 {
			go func() {
				<-start
				request := in
				if !sameClient {
					request.ClientID = uuid.NewString()
				}
				detail, err := s.Create(ctx, service.TicketActor{UserID: owner}, request)
				if err != nil {
					results <- outcome{err: err}
					return
				}
				results <- outcome{id: detail.Ticket.ID}
			}()
		}
		close(start)
		var id int64
		successes := 0
		for range 16 {
			result := <-results
			if result.err != nil {
				require.False(t, sameClient)
				require.ErrorIs(t, result.err, service.ErrTicketDailyLimit)
			} else {
				successes++
				if id == 0 {
					id = result.id
				}
				require.Equal(t, id, result.id)
			}
		}
		if sameClient {
			require.Equal(t, 16, successes)
		} else {
			require.Equal(t, 1, successes)
		}
		var tickets, messages int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_tickets WHERE user_id = $1", owner).Scan(&tickets))
		require.Equal(t, 1, tickets)
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id = $1", id).Scan(&messages))
		require.Equal(t, 1, messages)
		_, err := s.Update(ctx, service.TicketActor{UserID: owner}, id, service.UpdateTicketInput{Status: ticketPGString("closed")})
		require.NoError(t, err)
		_, err = s.Create(ctx, service.TicketActor{UserID: owner}, ticketPGInput())
		require.ErrorIs(t, err, service.ErrTicketDailyLimit)
		_, err = s.Update(ctx, service.TicketActor{UserID: owner}, id, service.UpdateTicketInput{Status: ticketPGString("open")})
		require.NoError(t, err)
	}
	// An administrator using the user creation endpoint has the same daily quota.
	ticketPGCreate(t, s, ctx, 99)
	_, err := s.Create(ctx, service.TicketActor{UserID: 99}, ticketPGInput())
	require.ErrorIs(t, err, service.ErrTicketDailyLimit)
	// The database itself prevents bypassing the service's precheck.
	_, err = db.ExecContext(ctx, `INSERT INTO support_tickets (user_id,subject,category,priority,client_id,request_hash)
		VALUES (1,'direct','api','normal',$1,'hash')`, uuid.NewString())
	var duplicate *pq.Error
	require.ErrorAs(t, err, &duplicate)
	require.Equal(t, pq.ErrorCode("23505"), duplicate.Code)
	require.Equal(t, "support_tickets_user_day_unique", duplicate.Constraint)
}

func TestTicketPostgresMidnightIdempotencyAndStats(t *testing.T) {
	db, s, ctx := ticketPostgres(t)
	actor := service.TicketActor{UserID: 1}
	in := ticketPGInput()
	var before, after string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT ('2026-09-24T15:59:59Z'::timestamptz AT TIME ZONE 'Asia/Shanghai')::date::text,
		('2026-09-24T16:00:00Z'::timestamptz AT TIME ZONE 'Asia/Shanghai')::date::text`).Scan(&before, &after))
	require.Equal(t, "2026-09-24", before)
	require.Equal(t, "2026-09-25", after)
	first, err := s.Create(ctx, actor, in)
	require.NoError(t, err)
	// Advance the fixture across the day boundary without changing the machine clock.
	_, err = db.ExecContext(ctx, `UPDATE support_tickets SET creation_day = (clock_timestamp() AT TIME ZONE 'Asia/Shanghai')::date - 1,
		status = 'closed' WHERE id = $1`, first.Ticket.ID)
	require.NoError(t, err)
	stats, err := s.Stats(ctx, actor)
	require.NoError(t, err)
	require.True(t, stats.CanCreate)
	require.Nil(t, stats.NextCreateAt)
	replay, err := s.Create(ctx, actor, in)
	require.NoError(t, err)
	require.Equal(t, first.Ticket.ID, replay.Ticket.ID)
	require.Equal(t, "closed", replay.Ticket.Status)
	today := ticketPGCreate(t, s, ctx, 1)
	require.NotEqual(t, first.Ticket.ID, today.Ticket.ID)
	replay, err = s.Create(ctx, actor, in)
	require.NoError(t, err)
	require.Equal(t, first.Ticket.ID, replay.Ticket.ID)
	changed := in
	changed.Contact = "different-contact"
	_, err = s.Create(ctx, actor, changed)
	require.ErrorIs(t, err, service.ErrTicketClientConflict)
	stats, err = s.Stats(ctx, actor)
	require.NoError(t, err)
	require.False(t, stats.CanCreate)
	require.NotNil(t, stats.NextCreateAt)
	var expected time.Time
	require.NoError(t, db.QueryRowContext(ctx, `SELECT (((clock_timestamp() AT TIME ZONE 'Asia/Shanghai')::date + 1)::timestamp AT TIME ZONE 'Asia/Shanghai')`).Scan(&expected))
	require.True(t, expected.Equal(*stats.NextCreateAt))
	require.Equal(t, time.UTC, stats.NextCreateAt.Location())
	_, err = s.Create(ctx, actor, ticketPGInput())
	require.ErrorIs(t, err, service.ErrTicketDailyLimit)
}

func TestTicketPostgresReadCursorAndSharedAdminUnread(t *testing.T) {
	_, s, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	otherAdmin := service.TicketActor{UserID: 100, Admin: true}
	created := ticketPGCreate(t, s, ctx, 1)
	id := created.Ticket.ID
	_, err := s.Detail(ctx, admin, id, 0)
	require.NoError(t, err)
	require.NoError(t, s.MarkRead(ctx, admin, id, created.Ticket.LastMessageID))
	team, err := s.Detail(ctx, otherAdmin, id, 0)
	require.NoError(t, err)
	require.Zero(t, team.Ticket.UnreadCount)
	_, err = s.Reply(ctx, admin, id, service.ReplyTicketInput{Content: "first reply", ClientID: uuid.NewString()})
	require.NoError(t, err)
	seen, err := s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), seen.Ticket.UnreadCount)
	newer, err := s.Reply(ctx, admin, id, service.ReplyTicketInput{Content: "concurrent arrival", ClientID: uuid.NewString()})
	require.NoError(t, err)
	require.ErrorIs(t, s.MarkRead(ctx, owner, id, newer.Ticket.LastMessageID), service.ErrTicketReadCursor)
	require.NoError(t, s.MarkRead(ctx, owner, id, seen.Ticket.LastMessageID))
	latest, err := s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), latest.Ticket.UnreadCount)
	stillUnread, err := s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), stillUnread.Ticket.UnreadCount)
	require.NoError(t, s.MarkRead(ctx, owner, id, latest.Ticket.LastMessageID))
	require.NoError(t, s.MarkRead(ctx, owner, id, seen.Ticket.LastMessageID))
	latest, err = s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Zero(t, latest.Ticket.UnreadCount)
	foreign := ticketPGCreate(t, s, ctx, 2)
	require.ErrorIs(t, s.MarkRead(ctx, owner, id, foreign.Ticket.LastMessageID), service.ErrTicketReadCursor)
	_, err = s.Detail(ctx, service.TicketActor{UserID: 2}, id, 0)
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	_, err = s.Detail(ctx, owner, id, foreign.Ticket.LastMessageID)
	require.Error(t, err)
}

func TestTicketPostgresReplyLocksIdempotencyAndLimits(t *testing.T) {
	db, s, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	id := ticketPGCreate(t, s, ctx, 1).Ticket.ID
	_, err := s.Update(ctx, admin, id, service.UpdateTicketInput{Status: ticketPGString("resolved")})
	require.NoError(t, err)
	in := service.ReplyTicketInput{Content: "reopen with reply", ClientID: uuid.NewString()}
	start := make(chan struct{})
	results := make(chan error, 12)
	for range 12 {
		go func() { <-start; _, err := s.Reply(ctx, owner, id, in); results <- err }()
	}
	close(start)
	for range 12 {
		require.NoError(t, <-results)
	}
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1 AND client_id=$2", id, in.ClientID).Scan(&count))
	require.Equal(t, 1, count)
	detail, err := s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, "open", detail.Ticket.Status)
	// Hold a close transaction ahead of a reply, forcing the reply to inspect the
	// locked current state instead of the state seen before waiting for the row.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, "UPDATE support_tickets SET status='closed' WHERE id=$1", id)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := s.Reply(ctx, owner, id, service.ReplyTicketInput{Content: "must not appear", ClientID: uuid.NewString()})
		done <- err
	}()
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-done, service.ErrTicketClosed)
	replay, err := s.Reply(ctx, owner, id, in)
	require.NoError(t, err)
	require.Equal(t, "closed", replay.Ticket.Status)
	changed := in
	changed.Content = "changed"
	_, err = s.Reply(ctx, owner, id, changed)
	require.ErrorIs(t, err, service.ErrTicketClientConflict)
	// A successful event insert followed by a failed reply must roll back both.
	_, err = s.Update(ctx, owner, id, service.UpdateTicketInput{Status: ticketPGString("open")})
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1", id).Scan(&count))
	r := &ticketRepository{db: db}
	err = r.Mutate(ctx, owner, id, uuid.NewString(), "hash", func(ticket *service.Ticket) (*service.TicketChange, error) {
		ticket.Status = "resolved"
		return &service.TicketChange{Ticket: ticket, Messages: []service.TicketMessage{{Kind: "event", AuthorRole: "system", Content: "event"}, {Kind: "reply", AuthorRole: "user", Content: ""}}}, nil
	})
	require.Error(t, err)
	var after int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1", id).Scan(&after))
	require.Equal(t, count, after)
	detail, err = s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, "open", detail.Ticket.Status)
	// Per-actor write throttling is shared across instances through PostgreSQL.
	id2 := ticketPGCreate(t, s, ctx, 2).Ticket.ID
	for range 20 {
		_, err = s.Reply(ctx, service.TicketActor{UserID: 2}, id2, service.ReplyTicketInput{Content: "reply", ClientID: uuid.NewString()})
		require.NoError(t, err)
	}
	_, err = s.Reply(ctx, service.TicketActor{UserID: 2}, id2, service.ReplyTicketInput{Content: "limited", ClientID: uuid.NewString()})
	require.ErrorIs(t, err, service.ErrTicketRateLimit)
}

func TestTicketPostgresSearchAssignmentPrivacyAndLiveBalance(t *testing.T) {
	db, s, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	admin := service.TicketActor{UserID: 99, Admin: true}
	first := ticketPGCreate(t, s, ctx, 1)
	ticketPGCreate(t, s, ctx, 2)
	input := ticketPGInput()
	input.Subject = "Invoice check"
	input.Category = "billing"
	_, err := s.Create(ctx, service.TicketActor{UserID: 3}, input)
	require.NoError(t, err)
	for _, tc := range []struct {
		actor  service.TicketActor
		search string
		total  int64
	}{
		{owner, strconv.FormatInt(first.Ticket.ID, 10), 1}, {owner, "#" + strconv.FormatInt(first.Ticket.ID, 10), 1},
		{admin, "Invoice", 1}, {admin, "user2", 1}, {admin, "user3@example.test", 1},
		{owner, "user2@example.test", 0}, {owner, "Invoice", 0}, {admin, "%", 0}, {admin, "#99999999999999999999999", 0},
	} {
		_, total, err := s.List(ctx, tc.actor, service.TicketFilter{PaginationParams: pagination.DefaultPagination(), Search: tc.search})
		require.NoError(t, err, tc.search)
		require.Equal(t, tc.total, total, tc.search)
	}
	_, err = s.Update(ctx, admin, first.Ticket.ID, service.UpdateTicketInput{AssignedTo: ticketPGString("me"), Priority: ticketPGString("urgent"), Status: ticketPGString("in_progress")})
	require.NoError(t, err)
	items, total, err := s.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.DefaultPagination(), AssignedTo: "mine", Priority: "urgent", Status: "in_progress", Category: "api"})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(99), *items[0].AssigneeID)
	require.Equal(t, "user1@example.test", items[0].UserEmail)
	_, total, err = s.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.DefaultPagination(), AssignedTo: "unassigned"})
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	page1, _, err := s.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: 1, PageSize: 1}})
	require.NoError(t, err)
	page2, _, err := s.List(ctx, admin, service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: 2, PageSize: 1}})
	require.NoError(t, err)
	require.NotEqual(t, page1[0].ID, page2[0].ID)
	for _, balance := range []float64{12.5, 9.25} {
		_, err = db.ExecContext(ctx, "UPDATE users SET balance=$2 WHERE id=$1", 1, balance)
		require.NoError(t, err)
		detail, err := s.Detail(ctx, admin, first.Ticket.ID, 0)
		require.NoError(t, err)
		require.Equal(t, balance, detail.Requester.Balance)
		require.Equal(t, "user1", detail.Requester.Username)
		require.Equal(t, "contact-handle", detail.Ticket.Contact)
		private, err := s.Detail(ctx, owner, first.Ticket.ID, 0)
		require.NoError(t, err)
		require.Nil(t, private.Requester)
		require.Empty(t, private.Ticket.UserEmail)
	}
	_, err = s.Update(ctx, admin, first.Ticket.ID, service.UpdateTicketInput{AssignedTo: ticketPGString("unassigned"), Status: ticketPGString("closed")})
	require.NoError(t, err)
}

func TestTicketPostgresMessagePages(t *testing.T) {
	db, s, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	id := ticketPGCreate(t, s, ctx, 1).Ticket.ID
	_, err := db.ExecContext(ctx, `INSERT INTO support_ticket_messages (ticket_id,author_id,author_role,content,kind)
		SELECT $1,99,'admin','reply ' || n,'reply' FROM generate_series(1,120) n;
		`, id)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE support_tickets SET last_message_id=(SELECT MAX(id) FROM support_ticket_messages WHERE ticket_id=$1) WHERE id=$1`, id)
	require.NoError(t, err)
	seen := map[int64]bool{}
	var before int64
	for page, want := range []int{50, 50, 21} {
		detail, err := s.Detail(ctx, owner, id, before)
		require.NoError(t, err)
		require.Len(t, detail.Messages, want)
		require.Equal(t, page < 2, detail.HasMore)
		for i, m := range detail.Messages {
			require.False(t, seen[m.ID])
			seen[m.ID] = true
			if i > 0 {
				require.Greater(t, m.ID, detail.Messages[i-1].ID)
			}
		}
		before = detail.Messages[0].ID
	}
	require.Len(t, seen, 121)
}

func TestTicketPostgresConcurrentCloseAndReply(t *testing.T) {
	_, s, ctx := ticketPostgres(t)
	owner := service.TicketActor{UserID: 1}
	id := ticketPGCreate(t, s, ctx, 1).Ticket.ID
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := s.Update(ctx, owner, id, service.UpdateTicketInput{Status: ticketPGString("closed")})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, err := s.Reply(ctx, owner, id, service.ReplyTicketInput{Content: "race", ClientID: uuid.NewString()})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, service.ErrTicketClosed) {
			t.Fatal(fmt.Errorf("concurrent change: %w", err))
		}
	}
	detail, err := s.Detail(ctx, owner, id, 0)
	require.NoError(t, err)
	require.Equal(t, "closed", detail.Ticket.Status)
}

func TestTicketPostgresOwnerAndAdminAuthorization(t *testing.T) {
	db, s, ctx := ticketPostgres(t)
	created := ticketPGCreate(t, s, ctx, 1)
	foreign := service.TicketActor{UserID: 2}
	id := created.Ticket.ID
	_, err := s.Detail(ctx, foreign, id, 0)
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	_, err = s.Reply(ctx, foreign, id, service.ReplyTicketInput{Content: "unauthorized", ClientID: uuid.NewString()})
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	_, err = s.Update(ctx, foreign, id, service.UpdateTicketInput{Status: ticketPGString("closed")})
	require.ErrorIs(t, err, service.ErrTicketNotFound)
	require.ErrorIs(t, s.MarkRead(ctx, foreign, id, created.Ticket.LastMessageID), service.ErrTicketNotFound)
	items, total, err := s.List(ctx, foreign, service.TicketFilter{PaginationParams: pagination.DefaultPagination()})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
	stats, err := s.Stats(ctx, foreign)
	require.NoError(t, err)
	require.Zero(t, stats.Total)
	_, err = s.Detail(ctx, service.TicketActor{UserID: 2, Admin: true}, id, 0)
	require.Equal(t, 403, infraerrors.Code(err))
	_, err = s.Update(ctx, service.TicketActor{UserID: 1}, id, service.UpdateTicketInput{AssignedTo: ticketPGString("me")})
	require.Equal(t, 400, infraerrors.Code(err))
	// Assignment checks the current persisted role, even if a caller retained
	// an admin route identity from before demotion.
	_, err = db.ExecContext(ctx, "UPDATE users SET role='user' WHERE id=99")
	require.NoError(t, err)
	_, err = s.Update(ctx, service.TicketActor{UserID: 99, Admin: true}, id, service.UpdateTicketInput{AssignedTo: ticketPGString("me")})
	require.Equal(t, 403, infraerrors.Code(err))
	var messages, views int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE ticket_id=$1", id).Scan(&messages))
	require.Equal(t, 1, messages)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_views WHERE viewer_id=2").Scan(&views))
	require.Zero(t, views)
}

func ticketPGString(value string) *string { return &value }
