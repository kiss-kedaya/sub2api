package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type privateKeyExporterFunc func(context.Context, int64) (*wallet.AddressPrivateKeyExport, error)

func (f privateKeyExporterFunc) ExportAddressPrivateKey(ctx context.Context, id int64) (*wallet.AddressPrivateKeyExport, error) {
	return f(ctx, id)
}

func TestCryptoAddressPrivateKeyExportReturnsOnlySelectedKeyAndSafeAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const route = "/api/v1/admin/crypto/addresses/:id/export-private-key"
	fixtureKey := "0x" + strings.Repeat("1", 64)
	result := &wallet.AddressPrivateKeyExport{
		AddressID: 17, Address: "0x1111111111111111111111111111111111111111",
		Family: "evm", DerivationIndex: 8, PrivateKey: fixtureKey, Format: "hex",
	}
	capture := &cryptoAuditCapture{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Next()
	})
	router.Use(gin.HandlerFunc(middleware.NewAuditLogMiddleware(audit)))
	h := &CryptoHandler{privateKeyExporter: privateKeyExporterFunc(func(_ context.Context, id int64) (*wallet.AddressPrivateKeyExport, error) {
		require.EqualValues(t, 17, id)
		return result, nil
	})}
	router.POST(route, h.AdminExportAddressPrivateKey)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/crypto/addresses/17/export-private-key", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crypto-TOTP", "987654")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	audit.Stop()

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
	require.Equal(t, "no-cache", w.Header().Get("Pragma"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 6)
	require.Equal(t, fixtureKey, body["private_key"])
	require.EqualValues(t, 17, body["address_id"])
	require.NotContains(t, body, "deposit_mnemonic")
	require.NotContains(t, body, "hot_mnemonic")
	require.Empty(t, result.PrivateKey)
	require.Len(t, capture.records, 1)
	record := capture.records[0]
	require.Equal(t, "admin.crypto.address.private_key.export", record.Action)
	require.Equal(t, "<credential-bearing body omitted>", record.RequestBody)
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), fixtureKey)
	require.NotContains(t, string(encoded), "987654")
}

func TestCryptoAddressPrivateKeyExportRejectsUnsafeRequestsAndSanitizesFailures(t *testing.T) {
	for _, tc := range []struct {
		name, id, body string
		err            error
		result         *wallet.AddressPrivateKeyExport
		want, calls    int
	}{
		{name: "zero ID", id: "0", body: `{}`, want: 400},
		{name: "negative ID", id: "-1", body: `{}`, want: 400},
		{name: "non-numeric ID", id: "17 OR 1=1", body: `{}`, want: 400},
		{name: "overflow ID", id: strings.Repeat("9", 40), body: `{}`, want: 400},
		{name: "caller index", id: "17", body: `{"derivation_index":0}`, want: 400},
		{name: "caller seed", id: "17", body: `{"mnemonic":"fixture secret"}`, want: 400},
		{name: "multiple bodies", id: "17", body: `{} {}`, want: 400},
		{name: "oversized body", id: "17", body: `{"extra":"` + strings.Repeat("x", 5000) + `"}`, want: 400},
		{name: "address missing", id: "17", body: `{}`, err: wallet.ErrExportAddressNotFound, want: 404, calls: 1},
		{name: "secret error", id: "17", body: `{}`, err: errors.New("private wallet secret diagnostic"), want: 503, calls: 1},
		{name: "empty response", id: "17", body: `{}`, want: 503, calls: 1},
		{name: "wrong assignment", id: "17", body: `{}`, result: &wallet.AddressPrivateKeyExport{AddressID: 18, Address: "fixture", PrivateKey: "fixture secret"}, want: 503, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := &CryptoHandler{privateKeyExporter: privateKeyExporterFunc(func(context.Context, int64) (*wallet.AddressPrivateKeyExport, error) {
				calls++
				return tc.result, tc.err
			})}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = gin.Params{{Key: "id", Value: tc.id}}
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			h.AdminExportAddressPrivateKey(c)
			require.Equal(t, tc.want, w.Code)
			require.Equal(t, tc.calls, calls)
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			require.NotContains(t, w.Body.String(), "fixture secret")
			require.NotContains(t, w.Body.String(), "private wallet secret diagnostic")
		})
	}
}
