package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
)

const TicketMessagePageSize = 50

var (
	ErrTicketNotFound       = infraerrors.NotFound("TICKET_NOT_FOUND", "ticket not found")
	ErrTicketClosed         = infraerrors.Conflict("TICKET_CLOSED", "reopen the ticket before replying")
	ErrTicketClientConflict = infraerrors.Conflict("TICKET_CLIENT_ID_CONFLICT", "client_id was already used for a different request")
	ErrTicketDailyLimit     = infraerrors.Conflict("TICKET_DAILY_LIMIT", "今天已提交过工单，请在北京时间明天零点后再新建；已有工单可继续回复")
	ErrTicketRateLimit      = infraerrors.New(http.StatusTooManyRequests, "TICKET_RATE_LIMIT", "too many ticket changes; retry in one minute")
	ErrTicketReadCursor     = infraerrors.BadRequest("TICKET_READ_CURSOR_INVALID", "last_message_id must belong to a detail already fetched")
)

type Ticket struct {
	ID                 int64     `json:"id"`
	UserID             int64     `json:"user_id"`
	Subject            string    `json:"subject"`
	Contact            string    `json:"contact"`
	Category           string    `json:"category"`
	Priority           string    `json:"priority"`
	Status             string    `json:"status"`
	AssigneeID         *int64    `json:"assignee_id"`
	AssigneeName       string    `json:"assignee_name"`
	UserName           string    `json:"user_name,omitempty"`
	UserEmail          string    `json:"user_email,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	LastMessageAt      time.Time `json:"last_message_at"`
	LastMessagePreview string    `json:"last_message_preview"`
	UnreadCount        int64     `json:"unread_count"`
	LastMessageID      int64     `json:"last_message_id"`
}

type TicketMessage struct {
	ID         int64          `json:"id"`
	TicketID   int64          `json:"ticket_id"`
	AuthorID   *int64         `json:"author_id"`
	AuthorRole string         `json:"author_role"`
	AuthorName string         `json:"author_name"`
	Content    string         `json:"content"`
	Kind       string         `json:"kind"`
	EventType  string         `json:"event_type,omitempty"`
	EventData  map[string]any `json:"event_data,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type TicketDetail struct {
	Ticket    *Ticket          `json:"ticket"`
	Messages  []TicketMessage  `json:"messages"`
	HasMore   bool             `json:"has_more"`
	Requester *TicketRequester `json:"requester,omitempty"`
}

type TicketRequester struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Balance   float64   `json:"balance"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type TicketStats struct {
	Total        int64      `json:"total"`
	Open         int64      `json:"open"`
	InProgress   int64      `json:"in_progress"`
	WaitingUser  int64      `json:"waiting_user"`
	Resolved     int64      `json:"resolved"`
	Closed       int64      `json:"closed"`
	Unread       int64      `json:"unread"`
	CanCreate    bool       `json:"can_create"`
	NextCreateAt *time.Time `json:"next_create_at"`
}

type TicketActor struct {
	UserID int64
	Admin  bool
}

func (a TicketActor) Role() string {
	if a.Admin {
		return "admin"
	}
	return "user"
}

type TicketFilter struct {
	pagination.PaginationParams
	Search     string
	Status     string
	Priority   string
	Category   string
	AssignedTo string
}

type CreateTicketInput struct {
	Subject  string `json:"subject"`
	Contact  string `json:"contact"`
	Content  string `json:"content"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	ClientID string `json:"client_id"`
}

type ReplyTicketInput struct {
	Content  string  `json:"content"`
	ClientID string  `json:"client_id"`
	Status   *string `json:"status"`
}

type UpdateTicketInput struct {
	Status     *string `json:"status"`
	Priority   *string `json:"priority"`
	AssignedTo *string `json:"assigned_to"`
}

// TicketChange is computed from the locked row, so concurrent replies cannot
// overwrite an intervening close or assignment with a stale snapshot.
type TicketChange struct {
	Ticket   *Ticket
	Messages []TicketMessage
}

type TicketRepository interface {
	List(context.Context, TicketActor, TicketFilter) ([]Ticket, int64, error)
	Stats(context.Context, TicketActor) (*TicketStats, error)
	Detail(context.Context, TicketActor, int64, int64) (*TicketDetail, error)
	Create(context.Context, TicketActor, CreateTicketInput, string) (int64, error)
	Mutate(context.Context, TicketActor, int64, string, string, func(*Ticket) (*TicketChange, error)) error
	MarkRead(context.Context, TicketActor, int64, int64) error
}

type TicketService struct {
	repo  TicketRepository
	users UserRepository
}

func NewTicketService(repo TicketRepository, users UserRepository) *TicketService {
	return &TicketService{repo: repo, users: users}
}

func (s *TicketService) authorize(ctx context.Context, actor TicketActor) error {
	if actor.UserID <= 0 {
		return infraerrors.Unauthorized("UNAUTHORIZED", "authentication required")
	}
	u, err := s.users.GetByID(ctx, actor.UserID)
	if err != nil {
		return err
	}
	if u == nil || !u.IsActive() || u.DeletedAt != nil {
		return infraerrors.Unauthorized("UNAUTHORIZED", "active user required")
	}
	if actor.Admin && !u.IsAdmin() {
		return infraerrors.Forbidden("FORBIDDEN", "admin access required")
	}
	return nil
}

func ticketInvalid(message string) error {
	return infraerrors.BadRequest("TICKET_INVALID_REQUEST", message)
}

func ticketText(value string, max int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= max
}

func ticketCategory(v string) bool {
	return v == "billing" || v == "api" || v == "account" || v == "other"
}
func ticketPriority(v string) bool { return v == "normal" || v == "high" || v == "urgent" }
func TicketActive(status string) bool {
	return status == "open" || status == "in_progress" || status == "waiting_user"
}
func ticketStatus(v string) bool { return TicketActive(v) || v == "resolved" || v == "closed" }

func ticketClientID(raw string) (string, error) {
	id, err := uuid.Parse(raw)
	if err != nil || len(raw) != 36 || id == uuid.Nil {
		return "", ticketInvalid("client_id must be a nonzero UUID")
	}
	return id.String(), nil
}

func ticketHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode ticket request: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func (s *TicketService) List(ctx context.Context, actor TicketActor, filter TicketFilter) ([]Ticket, int64, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, 0, err
	}
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 || filter.Page > math.MaxInt/filter.PageSize ||
		(filter.Status != "" && filter.Status != "active" && !ticketStatus(filter.Status)) || (filter.Priority != "" && !ticketPriority(filter.Priority)) ||
		(filter.Category != "" && !ticketCategory(filter.Category)) || !utf8.ValidString(filter.Search) || utf8.RuneCountInString(filter.Search) > 160 || strings.ContainsRune(filter.Search, 0) {
		return nil, 0, ticketInvalid("invalid ticket filters or pagination")
	}
	if filter.AssignedTo != "" && (!actor.Admin || (filter.AssignedTo != "mine" && filter.AssignedTo != "unassigned" && filter.AssignedTo != "all")) {
		return nil, 0, ticketInvalid("assigned_to must be mine, unassigned or all on admin routes")
	}
	return s.repo.List(ctx, actor, filter)
}

func (s *TicketService) Stats(ctx context.Context, actor TicketActor) (*TicketStats, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.repo.Stats(ctx, actor)
}

func (s *TicketService) Detail(ctx context.Context, actor TicketActor, id, before int64) (*TicketDetail, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if id <= 0 || before < 0 {
		return nil, ticketInvalid("invalid ticket or message ID")
	}
	return s.repo.Detail(ctx, actor, id, before)
}

func (s *TicketService) Create(ctx context.Context, actor TicketActor, in CreateTicketInput) (*TicketDetail, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if actor.Admin {
		return nil, ticketInvalid("create tickets through the user route")
	}
	if !ticketText(in.Subject, 160) || !ticketText(in.Content, 10000) || !ticketCategory(in.Category) || !ticketPriority(in.Priority) ||
		!utf8.ValidString(in.Contact) || utf8.RuneCountInString(in.Contact) > 200 || strings.ContainsRune(in.Contact, 0) {
		return nil, ticketInvalid("subject/content or category/priority is invalid")
	}
	var err error
	in.ClientID, err = ticketClientID(in.ClientID)
	if err != nil {
		return nil, err
	}
	hash, err := ticketHash(in)
	if err != nil {
		return nil, err
	}
	id, err := s.repo.Create(ctx, actor, in, hash)
	if err != nil {
		return nil, err
	}
	return s.repo.Detail(ctx, actor, id, 0)
}

func (s *TicketService) Reply(ctx context.Context, actor TicketActor, id int64, in ReplyTicketInput) (*TicketDetail, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if id <= 0 || !ticketText(in.Content, 10000) {
		return nil, ticketInvalid("content must contain 1-10000 characters")
	}
	status := "open"
	if actor.Admin {
		status = "waiting_user"
		if in.Status != nil {
			status = *in.Status
		}
		if status != "waiting_user" && status != "resolved" && status != "in_progress" {
			return nil, ticketInvalid("invalid admin reply status")
		}
	} else if in.Status != nil {
		return nil, ticketInvalid("users cannot set reply status")
	}
	var err error
	in.ClientID, err = ticketClientID(in.ClientID)
	if err != nil {
		return nil, err
	}
	// Explicit and implicit defaults have identical idempotency semantics.
	in.Status = &status
	hash, err := ticketHash(in)
	if err != nil {
		return nil, err
	}
	err = s.repo.Mutate(ctx, actor, id, in.ClientID, hash, func(t *Ticket) (*TicketChange, error) {
		if !actor.Admin && t.UserID != actor.UserID {
			return nil, ErrTicketNotFound
		}
		if t.Status == "closed" {
			return nil, ErrTicketClosed
		}
		change := &TicketChange{Ticket: t}
		ticketSetStatus(change, actor, status)
		change.Messages = append(change.Messages, TicketMessage{AuthorID: &actor.UserID, AuthorRole: actor.Role(), Kind: "reply", Content: in.Content})
		return change, nil
	})
	if err != nil {
		return nil, err
	}
	return s.repo.Detail(ctx, actor, id, 0)
}

func (s *TicketService) Update(ctx context.Context, actor TicketActor, id int64, in UpdateTicketInput) (*TicketDetail, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if id <= 0 || (in.Status == nil && in.Priority == nil && in.AssignedTo == nil) {
		return nil, ticketInvalid("no ticket changes specified")
	}
	if !actor.Admin && (in.Priority != nil || in.AssignedTo != nil || in.Status == nil || (*in.Status != "open" && *in.Status != "closed")) {
		return nil, ticketInvalid("users may only close or reopen tickets")
	}
	if (in.Status != nil && !ticketStatus(*in.Status)) || (in.Priority != nil && !ticketPriority(*in.Priority)) ||
		(in.AssignedTo != nil && *in.AssignedTo != "me" && *in.AssignedTo != "unassigned") {
		return nil, ticketInvalid("invalid ticket update")
	}
	err := s.repo.Mutate(ctx, actor, id, "", "", func(t *Ticket) (*TicketChange, error) {
		if !actor.Admin && t.UserID != actor.UserID {
			return nil, ErrTicketNotFound
		}
		change := &TicketChange{Ticket: t}
		if in.Status != nil {
			ticketSetStatus(change, actor, *in.Status)
		}
		if in.Priority != nil && *in.Priority != t.Priority {
			change.Messages = append(change.Messages, ticketEvent(actor, "priority_changed", "优先级："+ticketPriorityName(t.Priority)+" → "+ticketPriorityName(*in.Priority), t.Priority, *in.Priority))
			t.Priority = *in.Priority
		}
		if in.AssignedTo != nil {
			var next *int64
			if *in.AssignedTo == "me" {
				next = &actor.UserID
			}
			if !ticketSameID(t.AssigneeID, next) {
				content := "已释放工单"
				if next != nil {
					content = "管理员已接单"
				}
				change.Messages = append(change.Messages, ticketEvent(actor, "assignment_changed", content, t.AssigneeID, next))
				t.AssigneeID = next
			}
		}
		return change, nil
	})
	if err != nil {
		return nil, err
	}
	return s.repo.Detail(ctx, actor, id, 0)
}

func (s *TicketService) MarkRead(ctx context.Context, actor TicketActor, id, lastID int64) error {
	if err := s.authorize(ctx, actor); err != nil {
		return err
	}
	if id <= 0 || lastID <= 0 {
		return ErrTicketReadCursor
	}
	return s.repo.MarkRead(ctx, actor, id, lastID)
}

func ticketSameID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func ticketEvent(actor TicketActor, kind, content string, from, to any) TicketMessage {
	return TicketMessage{AuthorID: &actor.UserID, AuthorRole: actor.Role(), Kind: "event", Content: content, EventType: kind, EventData: map[string]any{"from": from, "to": to}}
}

func ticketSetStatus(change *TicketChange, actor TicketActor, status string) {
	if change.Ticket.Status == status {
		return
	}
	change.Messages = append(change.Messages, ticketEvent(actor, "status_changed", "状态："+ticketStatusName(change.Ticket.Status)+" → "+ticketStatusName(status), change.Ticket.Status, status))
	change.Ticket.Status = status
}

func ticketStatusName(status string) string {
	return map[string]string{"open": "待处理", "in_progress": "处理中", "waiting_user": "等待用户", "resolved": "已解决", "closed": "已关闭"}[status]
}

func ticketPriorityName(priority string) string {
	return map[string]string{"normal": "普通", "high": "高", "urgent": "紧急"}[priority]
}
