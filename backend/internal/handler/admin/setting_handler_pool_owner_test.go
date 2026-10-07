package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type poolOwnerKeySettingsRepo struct {
	service.SettingRepository
	writes int
}

func (s *poolOwnerKeySettingsRepo) Set(context.Context, string, string) error { s.writes++; return nil }
func (s *poolOwnerKeySettingsRepo) Delete(context.Context, string) error      { s.writes++; return nil }

func TestGlobalAdminKeyMutationsRequireConfiguredPoolOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name             string
		adminID, ownerID int64
		wantStatus       int
	}{
		{"configured owner", 7, 7, http.StatusOK},
		{"other administrator", 8, 7, http.StatusForbidden},
		{"default deployment", 8, 0, http.StatusOK},
	} {
		for _, action := range []string{"regenerate", "delete"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				repo := &poolOwnerKeySettingsRepo{}
				h := &SettingHandler{settingService: service.NewSettingService(repo, &config.Config{})}
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/?account_pool_owner_user_id=8", nil)
				c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), tc.adminID, tc.ownerID))
				if action == "regenerate" {
					h.RegenerateAdminAPIKey(c)
				} else {
					h.DeleteAdminAPIKey(c)
				}
				require.Equal(t, tc.wantStatus, w.Code)
				if tc.wantStatus == http.StatusForbidden {
					require.Zero(t, repo.writes)
					require.NotContains(t, w.Body.String(), `"key":`)
				} else {
					require.Equal(t, 1, repo.writes)
				}
			})
		}
	}
}
