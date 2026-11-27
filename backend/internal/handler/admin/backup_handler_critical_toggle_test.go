package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type backupHandlerSettingRepo struct {
	mu     sync.Mutex
	values map[string]string
}

func newBackupHandlerSettingRepo(initial map[string]string) *backupHandlerSettingRepo {
	m := make(map[string]string, len(initial))
	for k, v := range initial {
		m[k] = v
	}
	return &backupHandlerSettingRepo{values: m}
}

func (r *backupHandlerSettingRepo) Get(_ context.Context, key string) (*service.Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.values[key]
	if !ok {
		return nil, service.ErrSettingNotFound
	}
	return &service.Setting{Key: key, Value: v}, nil
}

func (r *backupHandlerSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[key], nil
}

func (r *backupHandlerSettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *backupHandlerSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string)
	for _, k := range keys {
		if v, ok := r.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (r *backupHandlerSettingRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range values {
		r.values[k] = v
	}
	return nil
}

func (r *backupHandlerSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

func (r *backupHandlerSettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.values, key)
	return nil
}

type handlerTestEncryptor struct{}

func (handlerTestEncryptor) Encrypt(plaintext string) (string, error) { return "ENC:" + plaintext, nil }
func (handlerTestEncryptor) Decrypt(ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, "ENC:"), nil
}

type handlerTestObjectStore struct{}

func (handlerTestObjectStore) Upload(context.Context, string, io.Reader, string) (int64, error) {
	return 0, nil
}
func (handlerTestObjectStore) UploadFile(context.Context, string, string, string) (int64, error) {
	return 10, nil
}
func (handlerTestObjectStore) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (handlerTestObjectStore) Delete(context.Context, string) error { return nil }
func (handlerTestObjectStore) PresignURL(context.Context, string, time.Duration) (string, error) {
	return "https://example.test/obj", nil
}
func (handlerTestObjectStore) HeadBucket(context.Context) error { return nil }

type handlerTestDumper struct {
	excludeTables []string
}

func (d *handlerTestDumper) Dump(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("-- dump\n")), nil
}
func (d *handlerTestDumper) DumpWithOptions(_ context.Context, opts service.BackupDumpOptions) (io.ReadCloser, error) {
	d.excludeTables = append([]string(nil), opts.ExcludeTableData...)
	return io.NopCloser(strings.NewReader("-- critical dump\n")), nil
}
func (d *handlerTestDumper) Restore(context.Context, io.Reader) error { return nil }

func TestBackupScheduleEndpointPersistsCriticalOnlyAndReportsServerCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("default server omits server flags and round-trips critical_only", func(t *testing.T) {
		settings := newBackupHandlerSettingRepo(nil)
		svc := service.NewBackupService(settings, &config.Config{}, handlerTestEncryptor{}, nil, &handlerTestDumper{})
		h := NewBackupHandler(svc, nil, nil)

		// PUT schedule with critical_only=true (enabled=false avoids needing cronSched started)
		wPut := httptest.NewRecorder()
		cPut, _ := gin.CreateTestContext(wPut)
		cPut.Request = httptest.NewRequest("PUT", "/api/v1/admin/backups/schedule",
			strings.NewReader(`{"enabled":false,"cron_expr":"0 3 * * *","retain_days":7,"retain_count":5,"critical_only":true}`))
		cPut.Request.Header.Set("Content-Type", "application/json")
		h.UpdateSchedule(cPut)

		require.Equal(t, 200, wPut.Code)
		require.Contains(t, wPut.Body.String(), `"critical_only":true`)
		require.NotContains(t, wPut.Body.String(), `"server_critical_forced"`)
		require.NotContains(t, wPut.Body.String(), `"age_encryption_enabled"`)

		// GET schedule reads back critical_only=true
		wGet := httptest.NewRecorder()
		cGet, _ := gin.CreateTestContext(wGet)
		cGet.Request = httptest.NewRequest("GET", "/api/v1/admin/backups/schedule", nil)
		h.GetSchedule(cGet)

		require.Equal(t, 200, wGet.Code)
		require.Contains(t, wGet.Body.String(), `"critical_only":true`)
	})

	t.Run("server-enforced critical config and age encryption are surfaced read-only", func(t *testing.T) {
		identity, err := age.GenerateX25519Identity()
		require.NoError(t, err)
		pub := identity.Recipient().String()
		settings := newBackupHandlerSettingRepo(nil)
		svc := service.NewBackupService(settings, &config.Config{
			Backup: config.BackupConfig{
				ExcludeTableData: []string{"public.usage_logs"},
				AgeRecipients:    []string{pub},
			},
		}, handlerTestEncryptor{}, nil, &handlerTestDumper{})
		h := NewBackupHandler(svc, nil, nil)

		wGet := httptest.NewRecorder()
		cGet, _ := gin.CreateTestContext(wGet)
		cGet.Request = httptest.NewRequest("GET", "/api/v1/admin/backups/schedule", nil)
		h.GetSchedule(cGet)

		require.Equal(t, 200, wGet.Code)
		require.Contains(t, wGet.Body.String(), `"server_critical_forced":true`)
		require.Contains(t, wGet.Body.String(), `"age_encryption_enabled":true`)
		require.Contains(t, wGet.Body.String(), `"server_age_encryption_forced":true`)
		require.Contains(t, wGet.Body.String(), `"server_age_recipient_count":1`)
		require.NotContains(t, wGet.Body.String(), pub, "server env recipients must not be echoed in schedule response")
	})

	t.Run("admin age_recipients and max_rate_mbps round-trip and reject private keys", func(t *testing.T) {
		identity, err := age.GenerateX25519Identity()
		require.NoError(t, err)
		pub := identity.Recipient().String()
		secret := identity.String()

		settings := newBackupHandlerSettingRepo(nil)
		svc := service.NewBackupService(settings, &config.Config{}, handlerTestEncryptor{}, nil, &handlerTestDumper{})
		h := NewBackupHandler(svc, nil, nil)

		// Rejects private key without echoing it.
		wBad := httptest.NewRecorder()
		cBad, _ := gin.CreateTestContext(wBad)
		cBad.Request = httptest.NewRequest("PUT", "/api/v1/admin/backups/schedule",
			strings.NewReader(`{"enabled":false,"cron_expr":"0 3 * * *","retain_days":7,"retain_count":5,"age_recipients":["`+secret+`"]}`))
		cBad.Request.Header.Set("Content-Type", "application/json")
		h.UpdateSchedule(cBad)
		require.Equal(t, 400, wBad.Code)
		require.Contains(t, wBad.Body.String(), "BACKUP_AGE_PRIVATE_KEY_FORBIDDEN")
		require.NotContains(t, wBad.Body.String(), secret)

		// Rejects invalid max_rate_mbps.
		wRate := httptest.NewRecorder()
		cRate, _ := gin.CreateTestContext(wRate)
		cRate.Request = httptest.NewRequest("PUT", "/api/v1/admin/backups/schedule",
			strings.NewReader(`{"enabled":false,"cron_expr":"0 3 * * *","retain_days":7,"retain_count":5,"max_rate_mbps":501}`))
		cRate.Request.Header.Set("Content-Type", "application/json")
		h.UpdateSchedule(cRate)
		require.Equal(t, 400, wRate.Code)
		require.Contains(t, wRate.Body.String(), "INVALID_BACKUP_RATE_LIMIT")

		// Saves valid public key and rate limit.
		wPut := httptest.NewRecorder()
		cPut, _ := gin.CreateTestContext(wPut)
		cPut.Request = httptest.NewRequest("PUT", "/api/v1/admin/backups/schedule",
			strings.NewReader(`{"enabled":false,"cron_expr":"0 3 * * *","retain_days":7,"retain_count":5,"critical_only":true,"age_recipients":["`+pub+`"],"max_rate_mbps":12}`))
		cPut.Request.Header.Set("Content-Type", "application/json")
		h.UpdateSchedule(cPut)

		require.Equal(t, 200, wPut.Code)
		require.Contains(t, wPut.Body.String(), pub)
		require.Contains(t, wPut.Body.String(), `"max_rate_mbps":12`)
		require.Contains(t, wPut.Body.String(), `"age_encryption_enabled":true`)
		require.NotContains(t, wPut.Body.String(), `"server_age_encryption_forced"`)

		// GET returns persisted values.
		wGet := httptest.NewRecorder()
		cGet, _ := gin.CreateTestContext(wGet)
		cGet.Request = httptest.NewRequest("GET", "/api/v1/admin/backups/schedule", nil)
		h.GetSchedule(cGet)
		require.Equal(t, 200, wGet.Code)
		require.Contains(t, wGet.Body.String(), pub)
		require.Contains(t, wGet.Body.String(), `"max_rate_mbps":12`)
		require.Contains(t, wGet.Body.String(), `"age_encryption_enabled":true`)
	})
}

func TestBackupCreateEndpointForwardsCriticalOnlyOption(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s3JSON, err := json.Marshal(service.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret",
		Prefix:          "backups/",
	})
	require.NoError(t, err)

	t.Run("critical_only=true starts critical snapshot", func(t *testing.T) {
		settings := newBackupHandlerSettingRepo(map[string]string{
			"backup_s3_config": string(s3JSON),
		})
		dumper := &handlerTestDumper{}
		factory := func(context.Context, *service.BackupS3Config) (service.BackupObjectStore, error) {
			return handlerTestObjectStore{}, nil
		}
		svc := service.NewBackupService(settings, &config.Config{
			Database: config.DatabaseConfig{DBName: "sub2api"},
			Totp:     config.TotpConfig{EncryptionKeyConfigured: true},
		}, handlerTestEncryptor{}, factory, dumper)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups",
			strings.NewReader(`{"expire_days":7,"critical_only":true}`))
		c.Request.Header.Set("Content-Type", "application/json")

		NewBackupHandler(svc, nil, nil).CreateBackup(c)

		require.Equal(t, 202, w.Code)
		require.Contains(t, w.Body.String(), `"scope":"critical"`)
		require.Contains(t, w.Body.String(), `.critical.sql.gz`)
		svc.Stop()
		require.Equal(t, service.DefaultCriticalExcludeTableData, dumper.excludeTables)
	})

	t.Run("critical_only=false keeps whole-database dump", func(t *testing.T) {
		settings := newBackupHandlerSettingRepo(map[string]string{
			"backup_s3_config": string(s3JSON),
		})
		dumper := &handlerTestDumper{}
		factory := func(context.Context, *service.BackupS3Config) (service.BackupObjectStore, error) {
			return handlerTestObjectStore{}, nil
		}
		svc := service.NewBackupService(settings, &config.Config{
			Database: config.DatabaseConfig{DBName: "sub2api"},
			Totp:     config.TotpConfig{EncryptionKeyConfigured: true},
		}, handlerTestEncryptor{}, factory, dumper)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups",
			strings.NewReader(`{"expire_days":7,"critical_only":false}`))
		c.Request.Header.Set("Content-Type", "application/json")

		NewBackupHandler(svc, nil, nil).CreateBackup(c)

		require.Equal(t, 202, w.Code)
		require.NotContains(t, w.Body.String(), `"scope":"critical"`)
		require.NotContains(t, w.Body.String(), `.critical.sql.gz`)
		svc.Stop()
		require.Empty(t, dumper.excludeTables)
	})

	t.Run("manual backup inherits admin-configured age_recipients from schedule", func(t *testing.T) {
		identity, err := age.GenerateX25519Identity()
		require.NoError(t, err)
		scheduleJSON, err := json.Marshal(service.BackupScheduleConfig{
			CriticalOnly:  true,
			AgeRecipients: []string{identity.Recipient().String()},
			MaxRateMBps:   10,
		})
		require.NoError(t, err)
		settings := newBackupHandlerSettingRepo(map[string]string{
			"backup_s3_config": string(s3JSON),
			"backup_schedule":  string(scheduleJSON),
		})
		dumper := &handlerTestDumper{}
		factory := func(context.Context, *service.BackupS3Config) (service.BackupObjectStore, error) {
			return handlerTestObjectStore{}, nil
		}
		svc := service.NewBackupService(settings, &config.Config{
			Database: config.DatabaseConfig{DBName: "sub2api"},
			Totp:     config.TotpConfig{EncryptionKeyConfigured: true},
		}, handlerTestEncryptor{}, factory, dumper)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups",
			strings.NewReader(`{"expire_days":7,"critical_only":true}`))
		c.Request.Header.Set("Content-Type", "application/json")

		NewBackupHandler(svc, nil, nil).CreateBackup(c)

		require.Equal(t, 202, w.Code)
		require.Contains(t, w.Body.String(), `"encrypted":true`)
		require.Contains(t, w.Body.String(), `.critical.sql.gz.age`)
		svc.Stop()
	})
}

func TestBackupRestoreEndpointRefusesPlainCriticalFilenameWithoutScopeFlag(t *testing.T) {
	w := callRestoreEndpoint(t, `{"id":"snap1","status":"completed","file_name":"sub2api_20261126_030000.critical.sql.gz","s3_key":"backups/sub2api_20261126_030000.critical.sql.gz"}`)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "BACKUP_RESTORE_UNSUPPORTED")
}

func TestBackupScheduleEndpointReturnsErrorWhenStoredScheduleIsCorrupt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := newBackupHandlerSettingRepo(map[string]string{
		"backup_schedule": `{corrupt-json`,
	})
	svc := service.NewBackupService(settings, &config.Config{}, handlerTestEncryptor{}, nil, &handlerTestDumper{})
	h := NewBackupHandler(svc, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/admin/backups/schedule", nil)
	h.GetSchedule(c)

	require.Equal(t, 500, w.Code)
	require.Contains(t, w.Body.String(), "BACKUP_SCHEDULE_CORRUPT")
}

func TestBackupCreateEndpointRejectsInvalidCriticalOnlyType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := newBackupHandlerSettingRepo(nil)
	svc := service.NewBackupService(settings, &config.Config{}, handlerTestEncryptor{}, nil, &handlerTestDumper{})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/admin/backups", strings.NewReader(`{"critical_only":"yes"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	NewBackupHandler(svc, nil, nil).CreateBackup(c)

	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "Invalid request body")
}
