package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type emptyBackupSettingRepository struct{}

func (emptyBackupSettingRepository) Get(context.Context, string) (*service.Setting, error) {
	return nil, nil
}

func (emptyBackupSettingRepository) GetValue(context.Context, string) (string, error) {
	return "", nil
}

func (emptyBackupSettingRepository) Set(context.Context, string, string) error { return nil }

func (emptyBackupSettingRepository) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, nil
}

func (emptyBackupSettingRepository) SetMultiple(context.Context, map[string]string) error { return nil }

func (emptyBackupSettingRepository) GetAll(context.Context) (map[string]string, error) {
	return nil, nil
}

func (emptyBackupSettingRepository) Delete(context.Context, string) error { return nil }

func TestBackupCreateRejectsMalformedNonEmptyJSONBeforeStartingJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{`{"expire_days":`, `{"expire_days":"invalid"}`, `[]`, `false`} {
		t.Run(body, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			NewBackupHandler(nil, nil, nil).CreateBackup(c)
			require.Equal(t, 400, w.Code)
			require.Contains(t, w.Body.String(), "Invalid request body")
		})
	}
}

func TestBackupCreateAllowsEmptyBodyAndUsesDefaultRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	backupService := service.NewBackupService(emptyBackupSettingRepository{}, &config.Config{}, nil, nil, nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	NewBackupHandler(backupService, nil, nil).CreateBackup(c)

	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "BACKUP_S3_NOT_CONFIGURED")
	require.NotContains(t, w.Body.String(), "Invalid request body")
}
