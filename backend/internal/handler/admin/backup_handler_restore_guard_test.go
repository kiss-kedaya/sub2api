package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// callRestoreEndpoint drives BackupHandler.RestoreBackup the way the router does:
// authenticated admin, correct password, one stored backup record. The service
// is built without a dumper or object store, so a restore that slipped past the
// guard would fail differently (or panic) instead of returning the guard's error.
func callRestoreEndpoint(t *testing.T, recordJSON string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	const password = "test-admin-password"
	actor := &service.User{ID: 42, Role: service.RoleAdmin, Status: service.StatusActive}
	require.NoError(t, actor.SetPassword(password))

	settings := &settingHandlerRepoStub{values: map[string]string{"backup_records": "[" + recordJSON + "]"}}
	backupService := service.NewBackupService(settings, &config.Config{}, nil, nil, nil)
	userService := service.NewUserService(&globalTotpActorRepo{actor: actor}, settings, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups/snap1/restore", strings.NewReader(`{"password":"`+password+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "snap1"}}
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actor.ID})

	NewBackupHandler(backupService, userService, nil).RestoreBackup(c)
	return w
}

func TestBackupRestoreEndpointRefusesPartialAndEncryptedSnapshots(t *testing.T) {
	for name, record := range map[string]string{
		"encrypted":              `{"id":"snap1","status":"completed","s3_key":"backups/snap1.sql.gz.age","encrypted":true}`,
		"critical":               `{"id":"snap1","status":"completed","s3_key":"backups/snap1.sql.gz","scope":"critical"}`,
		"critical and encrypted": `{"id":"snap1","status":"completed","s3_key":"backups/snap1.sql.gz.age","scope":"critical","encrypted":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := callRestoreEndpoint(t, record)

			require.Equal(t, 400, w.Code)
			require.Contains(t, w.Body.String(), "BACKUP_RESTORE_UNSUPPORTED")
			require.Contains(t, w.Body.String(), "restored manually into an empty database")
		})
	}
}

// A record without the new fields is an ordinary whole-database backup: it must
// still get past the guard and be judged by the pre-existing checks.
func TestBackupRestoreEndpointLeavesWholeDatabaseBackupsAlone(t *testing.T) {
	w := callRestoreEndpoint(t, `{"id":"snap1","status":"completed","s3_key":"backups/snap1.sql.gz"}`)

	require.NotContains(t, w.Body.String(), "BACKUP_RESTORE_UNSUPPORTED")
	require.Contains(t, w.Body.String(), "BACKUP_S3_NOT_CONFIGURED", "the request must reach the object-store check")
}
