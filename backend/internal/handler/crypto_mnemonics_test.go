package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	cryptoTestDepositSeed = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	cryptoTestHotSeed     = "legal winner thank year wave sausage worth useful legal winner thank yellow"
)

type cryptoMnemonicExporterFunc func(context.Context) (*sweeper.MnemonicExport, error)

func (f cryptoMnemonicExporterFunc) ExportMnemonics(ctx context.Context) (*sweeper.MnemonicExport, error) {
	return f(ctx)
}

type cryptoAuditCapture struct{ records []*service.AuditLog }

func (r *cryptoAuditCapture) BatchInsert(_ context.Context, records []*service.AuditLog) (int64, error) {
	r.records = append(r.records, records...)
	return int64(len(records)), nil
}
func (r *cryptoAuditCapture) Insert(_ context.Context, entry *service.AuditLog) error {
	r.records = append(r.records, entry)
	return nil
}
func (*cryptoAuditCapture) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}
func (*cryptoAuditCapture) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, nil
}
func (*cryptoAuditCapture) Count(context.Context) (int64, error) { return 0, nil }
func (*cryptoAuditCapture) TruncateAll(context.Context) error    { return nil }
func (*cryptoAuditCapture) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestAdminExportMnemonicsReturnsTwoFieldsAndAuditsNoSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &cryptoAuditCapture{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 10})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Next()
	})
	router.Use(gin.HandlerFunc(middleware.NewAuditLogMiddleware(audit)))
	h := &CryptoHandler{mnemonicExporter: cryptoMnemonicExporterFunc(func(_ context.Context) (*sweeper.MnemonicExport, error) {
		return &sweeper.MnemonicExport{DepositMnemonic: cryptoTestDepositSeed, HotMnemonic: cryptoTestHotSeed}, nil
	})}
	router.POST("/api/v1/admin/crypto/wallet/export-mnemonics", h.AdminExportMnemonics)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Crypto-TOTP", "987654")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	audit.Stop()

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Header().Get("Cache-Control"), "no-store")
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	var response map[string]string
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response, 2)
	require.Equal(t, cryptoTestDepositSeed, response["deposit_mnemonic"])
	require.Equal(t, cryptoTestHotSeed, response["hot_mnemonic"])
	require.Len(t, capture.records, 1)
	require.Equal(t, "<credential-bearing body omitted>", capture.records[0].RequestBody)
	require.NotContains(t, capture.records[0].RequestBody, "private vault passphrase")
	require.NotContains(t, capture.records[0].CredentialMasked, "987654")
	require.NotContains(t, capture.records[0].CredentialMasked, "private")
}

func TestAdminExportMnemonicsNeverEchoesSensitiveErrorsOrOversizedBodies(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		fail bool
	}{
		"wallet error": {body: `{}`, fail: true},
		"over 4KB":     {body: `{"passphrase":"` + strings.Repeat("x", 5<<10) + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			called := false
			h := &CryptoHandler{mnemonicExporter: cryptoMnemonicExporterFunc(func(context.Context) (*sweeper.MnemonicExport, error) {
				called = true
				return nil, errors.New("sensitive-passphrase " + cryptoTestDepositSeed + " upstream response body")
			})}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			h.AdminExportMnemonics(c)
			require.NotContains(t, w.Body.String(), "sensitive-passphrase")
			require.NotContains(t, w.Body.String(), cryptoTestDepositSeed)
			require.NotContains(t, w.Body.String(), "upstream response body")
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			require.Equal(t, tc.fail, called)
		})
	}
}
