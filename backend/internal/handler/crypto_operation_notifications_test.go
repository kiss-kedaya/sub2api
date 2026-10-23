package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type operationNotificationFunc func(context.Context, string, string) error

func (f operationNotificationFunc) NotifyWalletOperation(ctx context.Context, action, phase string) error {
	return f(ctx, action, phase)
}

func TestWalletOperationIntentNotifiesButDoesNotAuthorizeOrDiscloseSecrets(t *testing.T) {
	for _, action := range []string{"sweep", "destination_change", "export_mnemonics"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			h := &CryptoHandler{operationNotifier: operationNotificationFunc(func(_ context.Context, gotAction, phase string) error {
				calls++
				require.Equal(t, action, gotAction)
				require.Equal(t, "requested", phase)
				return nil
			})}
			r := gin.New()
			r.POST("/intent", h.AdminWalletOperationIntent)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/intent", strings.NewReader(`{"action":"`+action+`"}`)))
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, 1, calls)
			require.JSONEq(t, `{"code":0,"message":"success","data":{"notified":true}}`, w.Body.String())
		})
	}
}

func TestWalletIntentAndDestinationChangesFailClosedOnNotificationFailure(t *testing.T) {
	failed := operationNotificationFunc(func(context.Context, string, string) error { return errors.New("private transport diagnostic") })
	store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
	h := &CryptoHandler{store: store, operationNotifier: failed}
	r := gin.New()
	r.POST("/intent", h.AdminWalletOperationIntent)
	r.PUT("/config", h.AdminUpdateConfig)
	for _, tt := range []struct{ method, path, body string }{
		{"POST", "/intent", `{"action":"export_mnemonics"}`},
		{"PUT", "/config", `{"hot_wallet_evm":"0x2222222222222222222222222222222222222222"}`},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), "CRYPTO_NOTIFICATION_UNAVAILABLE")
		require.NotContains(t, w.Body.String(), "private transport")
		require.Empty(t, store.writes)
	}
}

func TestWalletIntentRejectsUnrecognizedActionAndExtraPayload(t *testing.T) {
	h := &CryptoHandler{operationNotifier: operationNotificationFunc(func(context.Context, string, string) error { t.Fatal("invalid input must not reach Bark"); return nil })}
	r := gin.New()
	r.POST("/intent", h.AdminWalletOperationIntent)
	for _, body := range []string{`{"action":"verified"}`, `{"action":"sweep","seed":"private"}`, `{"action":"sweep"}{}`} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/intent", strings.NewReader(body)))
		require.Equal(t, 400, w.Code)
	}
}
