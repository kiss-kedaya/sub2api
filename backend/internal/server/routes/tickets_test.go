package routes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ticketRouteUsers struct {
	service.UserRepository
	users map[int64]*service.User
}

func (r *ticketRouteUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	return u, nil
}
func (r *ticketRouteUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (r *ticketRouteUsers) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

type ticketRouteRepo struct {
	service.TicketRepository
	filter   service.TicketFilter
	created  *service.CreateTicketInput
	lastRead int64
}

func (r *ticketRouteRepo) List(_ context.Context, a service.TicketActor, f service.TicketFilter) ([]service.Ticket, int64, error) {
	r.filter = f
	return []service.Ticket{{ID: 10, UserID: 7, Contact: "contact", UserName: "requester", UserEmail: "requester@example.test"}}, 1, nil
}
func (r *ticketRouteRepo) Stats(context.Context, service.TicketActor) (*service.TicketStats, error) {
	return &service.TicketStats{Total: 1, Open: 1, Unread: 1, CanCreate: false}, nil
}
func (r *ticketRouteRepo) Detail(_ context.Context, a service.TicketActor, id, before int64) (*service.TicketDetail, error) {
	if id != 10 || (!a.Admin && a.UserID != 7) {
		return nil, service.ErrTicketNotFound
	}
	return &service.TicketDetail{Ticket: &service.Ticket{ID: 10, UserID: 7, Contact: "contact", UserName: "requester", UserEmail: "requester@example.test"}, Messages: []service.TicketMessage{}, Requester: &service.TicketRequester{ID: 7, Username: "requester", Email: "requester@example.test", Balance: 12.5}}, nil
}
func (r *ticketRouteRepo) Create(_ context.Context, _ service.TicketActor, in service.CreateTicketInput, _ string) (int64, error) {
	r.created = &in
	return 10, nil
}
func (r *ticketRouteRepo) Mutate(_ context.Context, a service.TicketActor, id int64, _, _ string, fn func(*service.Ticket) (*service.TicketChange, error)) error {
	if id != 10 || (!a.Admin && a.UserID != 7) {
		return service.ErrTicketNotFound
	}
	_, err := fn(&service.Ticket{ID: 10, UserID: 7, Status: "open", Priority: "normal"})
	return err
}
func (r *ticketRouteRepo) MarkRead(_ context.Context, a service.TicketActor, id, last int64) error {
	if id != 10 || (!a.Admin && a.UserID != 7) {
		return service.ErrTicketNotFound
	}
	r.lastRead = last
	return nil
}

func ticketRouteEnvironment(t *testing.T) (*gin.Engine, map[int64]string, *ticketRouteRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := &ticketRouteUsers{users: map[int64]*service.User{
		7: {ID: 7, Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1},
		8: {ID: 8, Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1},
		9: {ID: 9, Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1},
	}}
	cfg := &config.Config{}
	cfg.JWT.Secret = "ticket-tests-only-local-signing-key"
	cfg.JWT.AccessTokenExpireMinutes = 60
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userService := service.NewUserService(users, nil, nil, nil)
	repo := &ticketRouteRepo{}
	h := &handler.Handlers{Admin: &handler.AdminHandlers{}, Ticket: handler.NewTicketHandler(service.NewTicketService(repo, users))}
	r := gin.New()
	v1 := r.Group("/api/v1")
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	RegisterUserRoutes(v1, h, middleware.NewJWTAuthMiddleware(auth, userService, nil, nil), audit, nil, nil)
	RegisterAdminRoutes(v1, h, middleware.NewAdminAuthMiddleware(auth, userService, nil, nil), audit, middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }), nil, nil)
	tokens := map[int64]string{}
	for id, u := range users.users {
		token, err := auth.GenerateToken(context.Background(), u)
		require.NoError(t, err)
		tokens[id] = token
	}
	return r, tokens, repo
}

func ticketRouteRequest(r *gin.Engine, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTicketRoutesRequireJWTAndAdminRole(t *testing.T) {
	r, tokens, _ := ticketRouteEnvironment(t)
	for _, prefix := range []string{"/api/v1/tickets", "/api/v1/admin/tickets"} {
		for _, route := range []struct{ method, path string }{{"GET", ""}, {"GET", "/stats"}, {"GET", "/10"}, {"POST", "/10/read"}, {"POST", "/10/replies"}, {"PATCH", "/10"}} {
			w := ticketRouteRequest(r, route.method, prefix+route.path, "", "{}")
			require.Equal(t, 401, w.Code, prefix+route.path)
			if strings.Contains(prefix, "/admin/") {
				w = ticketRouteRequest(r, route.method, prefix+route.path, tokens[7], "{}")
				require.Equal(t, 403, w.Code, prefix+route.path)
			}
		}
	}
	require.Equal(t, 401, ticketRouteRequest(r, "POST", "/api/v1/tickets", "", "{}").Code)
	require.Equal(t, 401, ticketRouteRequest(r, "GET", "/api/v1/tickets", "invalid.jwt", "").Code)
}

func TestTicketRoutesRejectOwnershipViolations(t *testing.T) {
	r, tokens, _ := ticketRouteEnvironment(t)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/10", ""}, {"GET", "/10?before_id=3", ""}, {"POST", "/10/read", `{"last_message_id":3}`},
		{"POST", "/10/replies", `{"content":"reply","client_id":"6b7a9b04-1952-491c-ae6d-a2da990d8a8b"}`}, {"PATCH", "/10", `{"status":"closed"}`},
	} {
		w := ticketRouteRequest(r, tc.method, "/api/v1/tickets"+tc.path, tokens[8], tc.body)
		require.Equal(t, 404, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "requester@example.test")
	}
}

func TestTicketRoutesEnvelopesAndRequesterPrivacy(t *testing.T) {
	r, tokens, repo := ticketRouteEnvironment(t)
	for _, tc := range []struct {
		path  string
		user  int64
		admin bool
	}{
		{"/api/v1/tickets", 7, false}, {"/api/v1/tickets/10", 7, false}, {"/api/v1/admin/tickets", 9, true}, {"/api/v1/admin/tickets/10", 9, true},
	} {
		w := ticketRouteRequest(r, "GET", tc.path, tokens[tc.user], "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var envelope struct {
			Code int                        `json:"code"`
			Data map[string]json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Equal(t, 0, envelope.Code)
		if !tc.admin {
			require.NotContains(t, w.Body.String(), "user_email")
			require.NotContains(t, w.Body.String(), "requester")
			require.NotContains(t, w.Body.String(), "balance")
		}
		if strings.HasSuffix(tc.path, "/10") {
			require.Contains(t, envelope.Data, "messages")
			require.Contains(t, envelope.Data, "has_more")
			if tc.admin {
				require.Contains(t, envelope.Data, "requester")
			}
		}
		if strings.HasSuffix(tc.path, "tickets") {
			for _, key := range []string{"items", "total", "page", "page_size", "pages"} {
				require.Contains(t, envelope.Data, key)
			}
			require.NotContains(t, w.Body.String(), "balance")
		}
	}
	w := ticketRouteRequest(r, "GET", "/api/v1/tickets/stats", tokens[7], "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"can_create":false`)
	require.Contains(t, w.Body.String(), `"next_create_at":null`)
	w = ticketRouteRequest(r, "GET", "/api/v1/admin/tickets?page=2&page_size=100&search=help&status=open&priority=urgent&category=billing&assigned_to=mine", tokens[9], "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, 100, repo.filter.PageSize)
	require.Equal(t, "mine", repo.filter.AssignedTo)
}

func TestTicketRoutesRequestValidation(t *testing.T) {
	r, tokens, _ := ticketRouteEnvironment(t)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "?page=-1", ""}, {"GET", "?page=x", ""}, {"GET", "?page_size=101", ""}, {"GET", "?page=999999999999999999999999", ""},
		{"GET", "?assigned_to=mine", ""}, {"GET", "/no", ""}, {"GET", "/10?before_id=-1", ""}, {"GET", "/10?before_id=x", ""},
		{"POST", "", `{"subject":"help","content":"body","category":"api","priority":"normal","client_id":"6b7a9b04-1952-491c-ae6d-a2da990d8a8b","creation_day":"2000-01-01"}`},
		{"POST", "", `{}`}, {"POST", "", `null`}, {"POST", "", `{} {}`}, {"POST", "/10/replies", `{"content":"reply","client_id":"invalid"}`},
		{"POST", "/10/read", `{"last_message_id":0}`}, {"POST", "/10/read", `{"last_message_id":1.5}`},
		{"PATCH", "/10", `{"priority":"urgent"}`}, {"PATCH", "/10", `{"assigned_to":"me"}`}, {"PATCH", "/10", `{"status":"resolved"}`},
	} {
		w := ticketRouteRequest(r, tc.method, "/api/v1/tickets"+tc.path, tokens[7], tc.body)
		require.Equal(t, 400, w.Code, tc.path+" "+tc.body)
	}
	body := `{"subject":"help","content":"` + strings.Repeat("x", 140000) + `"}`
	require.Equal(t, 400, ticketRouteRequest(r, "POST", "/api/v1/tickets", tokens[7], body).Code)
}

func TestTicketRoutesContactAndExplicitRead(t *testing.T) {
	r, tokens, repo := ticketRouteEnvironment(t)
	body := `{"subject":"help","content":"body","contact":"tg:contact","category":"api","priority":"normal","client_id":"6b7a9b04-1952-491c-ae6d-a2da990d8a8b"}`
	w := ticketRouteRequest(r, "POST", "/api/v1/tickets", tokens[7], body)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "tg:contact", repo.created.Contact)
	require.Zero(t, repo.lastRead)
	w = ticketRouteRequest(r, "GET", "/api/v1/tickets/10", tokens[7], "")
	require.Equal(t, 200, w.Code)
	require.Zero(t, repo.lastRead)
	w = ticketRouteRequest(r, "POST", "/api/v1/tickets/10/read", tokens[7], `{"last_message_id":3}`)
	require.Equal(t, 200, w.Code)
	require.Equal(t, int64(3), repo.lastRead)
	w = ticketRouteRequest(r, "PATCH", "/api/v1/admin/tickets/10", tokens[9], `{"status":"closed"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
}
