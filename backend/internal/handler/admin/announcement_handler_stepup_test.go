package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// announcementStepUpRepo 是公告仓储桩：记录写调用次数，GetByID 返回预置公告。
type announcementStepUpRepo struct {
	item       *service.Announcement
	createCall int
	updateCall int
}

func (r *announcementStepUpRepo) Create(_ context.Context, a *service.Announcement) error {
	r.createCall++
	item := *a
	if item.ID == 0 {
		item.ID = 1
	}
	r.item = &item
	return nil
}

func (r *announcementStepUpRepo) GetByID(_ context.Context, _ int64) (*service.Announcement, error) {
	if r.item == nil {
		return nil, service.ErrAnnouncementNotFound
	}
	item := *r.item
	return &item, nil
}

func (r *announcementStepUpRepo) Update(_ context.Context, a *service.Announcement) error {
	r.updateCall++
	item := *a
	r.item = &item
	return nil
}

func (r *announcementStepUpRepo) Delete(context.Context, int64) error { return nil }

func (r *announcementStepUpRepo) List(context.Context, pagination.PaginationParams, service.AnnouncementListFilters) ([]service.Announcement, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func (r *announcementStepUpRepo) ListActive(context.Context, time.Time) ([]service.Announcement, error) {
	return nil, nil
}

type announcementStepUpHarness struct {
	stepUpEnabled bool
	actorTotp     bool
	grant         bool
	adminAPIKey   bool
	existing      *service.Announcement
}

func setupAnnouncementStepUpRouter(t *testing.T, h announcementStepUpHarness) (*gin.Engine, *announcementStepUpRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	repo := &announcementStepUpRepo{item: h.existing}
	svc := service.NewAnnouncementService(repo, nil, nil, nil)

	actorRepo := &totpDemotionActorRepo{actor: &service.User{
		ID: 41, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: h.actorTotp,
	}}
	cache := &totpDemotionGrantCache{}
	if h.grant {
		cache.grantUserID = 41
		cache.grantSession = "admin-session"
	}
	totpSvc := service.NewTotpService(actorRepo, nil, cache, nil, nil, nil)
	userSvc := service.NewUserService(actorRepo, nil, nil, nil)
	enabled := "false"
	if h.stepUpEnabled {
		enabled = "true"
	}
	settings := service.NewSettingService(&settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyStepUpEnabled: enabled,
	}}, nil)

	handler := NewAnnouncementHandler(svc)
	handler.SetStepUpDependencies(totpSvc, userSvc, settings)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
		c.Set(middleware.ContextKeySessionID, "admin-session")
		if h.adminAPIKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		} else {
			c.Set("auth_method", "jwt")
		}
	})
	router.POST("/api/v1/admin/announcements", handler.Create)
	router.PUT("/api/v1/admin/announcements/:id", handler.Update)
	return router, repo
}

func activeAnnouncement() *service.Announcement {
	return &service.Announcement{ID: 7, Title: "在线公告", Content: "正文", Status: service.AnnouncementStatusActive}
}

func draftAnnouncement() *service.Announcement {
	return &service.Announcement{ID: 7, Title: "草稿", Content: "正文", Status: service.AnnouncementStatusDraft}
}

// 公告发布属用户可见内容的对外发布：未完成 step-up 必须被拦，完成后放行。
func TestAnnouncementPublishRequiresStepUp(t *testing.T) {
	tests := []struct {
		name       string
		harness    announcementStepUpHarness
		method     string
		payload    map[string]any
		wantStatus int
		wantCode   string
		wantCreate int
		wantUpdate int
	}{
		{
			name:       "publish_create_without_grant_is_blocked",
			harness:    announcementStepUpHarness{stepUpEnabled: true, actorTotp: true},
			method:     http.MethodPost,
			payload:    map[string]any{"title": "新公告", "content": "正文", "status": "active"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "publish_create_with_grant_succeeds",
			harness:    announcementStepUpHarness{stepUpEnabled: true, actorTotp: true, grant: true},
			method:     http.MethodPost,
			payload:    map[string]any{"title": "新公告", "content": "正文", "status": "active"},
			wantStatus: http.StatusOK,
			wantCreate: 1,
		},
		{
			name:       "publish_create_admin_api_key_is_forbidden",
			harness:    announcementStepUpHarness{stepUpEnabled: true, actorTotp: true, grant: true, adminAPIKey: true},
			method:     http.MethodPost,
			payload:    map[string]any{"title": "新公告", "content": "正文", "status": "active"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
		},
		{
			name:       "draft_create_does_not_require_step_up",
			harness:    announcementStepUpHarness{stepUpEnabled: true},
			method:     http.MethodPost,
			payload:    map[string]any{"title": "草稿", "content": "正文"},
			wantStatus: http.StatusOK,
			wantCreate: 1,
		},
		{
			name:       "publish_create_keeps_legacy_behavior_when_step_up_disabled",
			harness:    announcementStepUpHarness{stepUpEnabled: false, adminAPIKey: true},
			method:     http.MethodPost,
			payload:    map[string]any{"title": "新公告", "content": "正文", "status": "active"},
			wantStatus: http.StatusOK,
			wantCreate: 1,
		},
		{
			name:       "edit_live_announcement_without_status_change_is_blocked",
			harness:    announcementStepUpHarness{stepUpEnabled: true, actorTotp: true, existing: activeAnnouncement()},
			method:     http.MethodPut,
			payload:    map[string]any{"content": "改后的正文"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "edit_live_announcement_with_grant_succeeds",
			harness:    announcementStepUpHarness{stepUpEnabled: true, actorTotp: true, grant: true, existing: activeAnnouncement()},
			method:     http.MethodPut,
			payload:    map[string]any{"content": "改后的正文"},
			wantStatus: http.StatusOK,
			wantUpdate: 1,
		},
		{
			name:       "unpublish_live_announcement_does_not_require_step_up",
			harness:    announcementStepUpHarness{stepUpEnabled: true, existing: activeAnnouncement()},
			method:     http.MethodPut,
			payload:    map[string]any{"status": "draft"},
			wantStatus: http.StatusOK,
			wantUpdate: 1,
		},
		{
			name:       "edit_draft_does_not_require_step_up",
			harness:    announcementStepUpHarness{stepUpEnabled: true, existing: draftAnnouncement()},
			method:     http.MethodPut,
			payload:    map[string]any{"title": "草稿改名"},
			wantStatus: http.StatusOK,
			wantUpdate: 1,
		},
		{
			name:       "publish_existing_draft_without_grant_is_blocked",
			harness:    announcementStepUpHarness{stepUpEnabled: true, actorTotp: true, existing: draftAnnouncement()},
			method:     http.MethodPut,
			payload:    map[string]any{"status": "active"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, repo := setupAnnouncementStepUpRouter(t, tc.harness)
			body, err := json.Marshal(tc.payload)
			require.NoError(t, err)
			path := "/api/v1/admin/announcements"
			if tc.method == http.MethodPut {
				path = "/api/v1/admin/announcements/7"
			}
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			if tc.wantCode != "" {
				require.Contains(t, w.Body.String(), tc.wantCode)
			}
			require.Equal(t, tc.wantCreate, repo.createCall)
			require.Equal(t, tc.wantUpdate, repo.updateCall)
		})
	}
}
