package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

const ticketTestUUID = "6b7a9b04-1952-491c-ae6d-a2da990d8a8b"

type ticketUsersStub struct {
	UserRepository
	user *User
	err  error
}

func (s *ticketUsersStub) GetByID(context.Context, int64) (*User, error) { return s.user, s.err }

type ticketRepoStub struct {
	TicketRepository
	ticket   Ticket
	messages []TicketMessage
	created  *CreateTicketInput
	hash     string
	listed   bool
	mutated  bool
}

func (r *ticketRepoStub) Create(_ context.Context, _ TicketActor, in CreateTicketInput, hash string) (int64, error) {
	r.created, r.hash = &in, hash
	return 10, nil
}
func (r *ticketRepoStub) Detail(context.Context, TicketActor, int64, int64) (*TicketDetail, error) {
	ticket := r.ticket
	return &TicketDetail{Ticket: &ticket, Messages: r.messages}, nil
}
func (r *ticketRepoStub) Mutate(_ context.Context, _ TicketActor, _ int64, _, hash string, fn func(*Ticket) (*TicketChange, error)) error {
	ticket := r.ticket
	plan, err := fn(&ticket)
	if err != nil {
		return err
	}
	r.ticket, r.messages, r.hash, r.mutated = *plan.Ticket, plan.Messages, hash, true
	return nil
}
func (r *ticketRepoStub) List(context.Context, TicketActor, TicketFilter) ([]Ticket, int64, error) {
	r.listed = true
	return []Ticket{r.ticket}, 1, nil
}

func supportTicketTestService(admin bool) (*TicketService, *ticketRepoStub, TicketActor) {
	role := RoleUser
	if admin {
		role = RoleAdmin
	}
	repo := &ticketRepoStub{ticket: Ticket{ID: 10, UserID: 7, Status: "open", Priority: "normal"}}
	s := NewTicketService(repo, &ticketUsersStub{user: &User{ID: 7, Status: StatusActive, Role: role}})
	return s, repo, TicketActor{UserID: 7, Admin: admin}
}

func TestTicketCreateValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*CreateTicketInput)
		valid  bool
	}{
		{"valid", func(*CreateTicketInput) {}, true},
		{"unicode boundaries", func(in *CreateTicketInput) {
			in.Subject = strings.Repeat("界", 160)
			in.Content = strings.Repeat("界", 10000)
			in.Contact = strings.Repeat("界", 200)
		}, true},
		{"blank subject", func(in *CreateTicketInput) { in.Subject = "  \n" }, false},
		{"long subject", func(in *CreateTicketInput) { in.Subject = strings.Repeat("界", 161) }, false},
		{"blank content", func(in *CreateTicketInput) { in.Content = " \n\t" }, false},
		{"long content", func(in *CreateTicketInput) { in.Content = strings.Repeat("界", 10001) }, false},
		{"long contact", func(in *CreateTicketInput) { in.Contact = strings.Repeat("界", 201) }, false},
		{"nul contact", func(in *CreateTicketInput) { in.Contact = "a\x00b" }, false},
		{"invalid utf8", func(in *CreateTicketInput) { in.Content = string([]byte{0xff}) }, false},
		{"invalid category", func(in *CreateTicketInput) { in.Category = "unknown" }, false},
		{"invalid priority", func(in *CreateTicketInput) { in.Priority = "low" }, false},
		{"missing UUID", func(in *CreateTicketInput) { in.ClientID = "" }, false},
		{"nil UUID", func(in *CreateTicketInput) { in.ClientID = "00000000-0000-0000-0000-000000000000" }, false},
		{"compact UUID", func(in *CreateTicketInput) { in.ClientID = strings.ReplaceAll(ticketTestUUID, "-", "") }, false},
		{"uppercase UUID", func(in *CreateTicketInput) { in.ClientID = strings.ToUpper(ticketTestUUID) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, actor := supportTicketTestService(false)
			in := CreateTicketInput{Subject: "help", Content: "request", Category: "api", Priority: "normal", ClientID: ticketTestUUID}
			tc.change(&in)
			_, err := s.Create(context.Background(), actor, in)
			if !tc.valid {
				require.Equal(t, 400, infraerrors.Code(err))
				require.Nil(t, repo.created)
				return
			}
			require.NoError(t, err)
			require.Equal(t, ticketTestUUID, repo.created.ClientID)
			require.Len(t, repo.hash, 64)
		})
	}
}

func TestTicketServiceRejectsAnonymousAndNonAdmin(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func(*TicketService, TicketActor) error
	}{
		{"list", func(s *TicketService, a TicketActor) error {
			_, _, err := s.List(context.Background(), a, TicketFilter{})
			return err
		}},
		{"stats", func(s *TicketService, a TicketActor) error { _, err := s.Stats(context.Background(), a); return err }},
		{"detail", func(s *TicketService, a TicketActor) error {
			_, err := s.Detail(context.Background(), a, 10, 0)
			return err
		}},
		{"create", func(s *TicketService, a TicketActor) error {
			_, err := s.Create(context.Background(), a, CreateTicketInput{})
			return err
		}},
		{"reply", func(s *TicketService, a TicketActor) error {
			_, err := s.Reply(context.Background(), a, 10, ReplyTicketInput{})
			return err
		}},
		{"update", func(s *TicketService, a TicketActor) error {
			_, err := s.Update(context.Background(), a, 10, UpdateTicketInput{})
			return err
		}},
		{"read", func(s *TicketService, a TicketActor) error { return s.MarkRead(context.Background(), a, 10, 2) }},
	} {
		t.Run(action.name, func(t *testing.T) {
			s, _, _ := supportTicketTestService(false)
			require.Equal(t, 401, infraerrors.Code(action.run(s, TicketActor{})))
			require.Equal(t, 403, infraerrors.Code(action.run(s, TicketActor{UserID: 7, Admin: true})))
			s.users = &ticketUsersStub{user: &User{ID: 7, Status: "disabled", Role: RoleAdmin}}
			require.Equal(t, 401, infraerrors.Code(action.run(s, TicketActor{UserID: 7, Admin: true})))
		})
	}
}

func TestTicketReplyStateMachine(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, status := range []string{"open", "in_progress", "waiting_user", "resolved", "closed"} {
			t.Run(TicketActor{Admin: admin}.Role()+"/"+status, func(t *testing.T) {
				s, repo, actor := supportTicketTestService(admin)
				repo.ticket.Status = status
				detail, err := s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
				if status == "closed" {
					require.ErrorIs(t, err, ErrTicketClosed)
					require.False(t, repo.mutated)
					return
				}
				require.NoError(t, err)
				expected := "open"
				if admin {
					expected = "waiting_user"
				}
				require.Equal(t, expected, detail.Ticket.Status)
				require.Equal(t, "reply", detail.Messages[len(detail.Messages)-1].Kind)
				if status != expected {
					require.Equal(t, "status_changed", detail.Messages[0].EventType)
					require.Equal(t, status, detail.Messages[0].EventData["from"])
				}
			})
		}
	}
}

func TestTicketReplyPermissionsAndDefaults(t *testing.T) {
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: ticketTestString("resolved")})
	require.Equal(t, 400, infraerrors.Code(err))
	require.False(t, repo.mutated)
	repo.ticket.UserID = 8
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
	require.ErrorIs(t, err, ErrTicketNotFound)
	s, repo, actor = supportTicketTestService(true)
	for _, status := range []string{"waiting_user", "in_progress", "resolved", "open", "closed", "bad"} {
		_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: &status})
		if status == "open" || status == "closed" || status == "bad" {
			require.Equal(t, 400, infraerrors.Code(err))
		} else {
			require.NoError(t, err)
			require.Equal(t, status, repo.ticket.Status)
		}
	}
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
	require.NoError(t, err)
	defaultHash := repo.hash
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: ticketTestString("waiting_user")})
	require.NoError(t, err)
	require.Equal(t, defaultHash, repo.hash)
}

func TestTicketUpdatePermissionsAndEvents(t *testing.T) {
	for _, in := range []UpdateTicketInput{{}, {Status: ticketTestString("resolved")}, {Priority: ticketTestString("urgent")}, {AssignedTo: ticketTestString("me")}} {
		s, repo, a := supportTicketTestService(false)
		_, err := s.Update(context.Background(), a, 10, in)
		require.Equal(t, 400, infraerrors.Code(err))
		require.False(t, repo.mutated)
	}
	s, repo, a := supportTicketTestService(false)
	_, err := s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("closed")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.ticket.Status)
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("open")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.messages[0].EventData["from"])
	repo.ticket.UserID = 8
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("closed")})
	require.ErrorIs(t, err, ErrTicketNotFound)
	s, repo, a = supportTicketTestService(true)
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("closed"), Priority: ticketTestString("urgent"), AssignedTo: ticketTestString("me")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.ticket.Status)
	require.Equal(t, int64(7), *repo.ticket.AssigneeID)
	require.Len(t, repo.messages, 3)
	for _, m := range repo.messages {
		require.Equal(t, "event", m.Kind)
		require.NotEmpty(t, m.Content)
		require.Equal(t, "admin", m.AuthorRole)
	}
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{AssignedTo: ticketTestString("unassigned")})
	require.NoError(t, err)
	require.Nil(t, repo.ticket.AssigneeID)
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{AssignedTo: ticketTestString("123")})
	require.Equal(t, 400, infraerrors.Code(err))
}

func TestTicketFiltersAndCursorValidation(t *testing.T) {
	for _, change := range []func(*TicketFilter){
		func(f *TicketFilter) { f.Page = 0 }, func(f *TicketFilter) { f.PageSize = 101 }, func(f *TicketFilter) { f.Page = int(^uint(0) >> 1) },
		func(f *TicketFilter) { f.Status = "bad" }, func(f *TicketFilter) { f.Priority = "bad" }, func(f *TicketFilter) { f.Category = "bad" },
		func(f *TicketFilter) { f.AssignedTo = "mine" }, func(f *TicketFilter) { f.Search = strings.Repeat("界", 161) },
		func(f *TicketFilter) { f.Search = string([]byte{0xff}) },
	} {
		s, r, a := supportTicketTestService(false)
		f := TicketFilter{PaginationParams: pagination.DefaultPagination()}
		change(&f)
		_, _, err := s.List(context.Background(), a, f)
		require.Equal(t, 400, infraerrors.Code(err))
		require.False(t, r.listed)
	}
	s, _, a := supportTicketTestService(false)
	_, err := s.Detail(context.Background(), a, 10, -1)
	require.Equal(t, 400, infraerrors.Code(err))
	require.ErrorIs(t, s.MarkRead(context.Background(), a, 10, 0), ErrTicketReadCursor)
	s.users = &ticketUsersStub{err: errors.New("lookup failed")}
	_, err = s.Detail(context.Background(), a, 10, 0)
	require.EqualError(t, err, "lookup failed")
}

func ticketTestString(value string) *string { return &value }
