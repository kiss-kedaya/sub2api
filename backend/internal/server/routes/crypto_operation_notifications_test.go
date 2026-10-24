package routes

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

type walletRouteNotification struct {
	messages []string
	fail     bool
}

func (n *walletRouteNotification) PushToAdmins(_ context.Context, title, body, _ string) error {
	n.messages = append(n.messages, title+"\n"+body)
	if n.fail {
		return errors.New("offline")
	}
	return nil
}

func TestCryptoWalletClicksAndBackendVerificationHaveSeparateNotifications(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	push := &walletRouteNotification{}
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), push)
	intent := cryptoRouteRequestBody(h.router, "POST", "/api/v1/admin/crypto/security/operation-intent", `{"action":"export_mnemonics"}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, 200, intent.Code, intent.Body.String())
	require.Len(t, push.messages, 1)
	require.Contains(t, push.messages[0], "等待服务器检查支付授权")
	path := "/api/v1/admin/crypto/wallet/export-mnemonics"
	wrong := cryptoRouteRequest(h.router, http.MethodPost, path, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, 403, wrong.Code, wrong.Body.String())
	require.Len(t, push.messages, 1)
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	verified := cryptoRouteRequest(h.router, http.MethodPost, path, code, nil, "127.0.0.1:1234", "localhost")
	// The zero-value wallet cannot export, but 2FA succeeded and was reported
	// BEFORE any attempt to decrypt. No frontend "verified" event is trusted.
	require.Equal(t, 503, verified.Code, verified.Body.String())
	require.Len(t, push.messages, 2)
	require.Contains(t, push.messages[1], "服务器已验证当前支付授权")
	replay := cryptoRouteRequest(h.router, http.MethodPost, path, code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, 409, replay.Code, replay.Body.String())
	require.Len(t, push.messages, 2)
}

func TestCryptoWalletIntentKeepsJWTHTTPSAndReadOnlyGuards(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, test := range []string{"unauthenticated", "public http", "observer", "notifier offline", "read only"} {
		t.Run(test, func(t *testing.T) {
			push := &walletRouteNotification{}
			u := activeCryptoAdmin()
			h := newCryptoRouteHarness(t, u, push)
			peer, host := "127.0.0.1:1234", "localhost"
			switch test {
			case "unauthenticated":
				h.identity.authenticated = false
			case "public http":
				peer, host = "203.0.113.1:1234", "example.test"
			case "observer":
				u.ID = 95
			case "notifier offline":
				push.fail = true
			case "read only":
				t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
				t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
			}
			w := cryptoRouteRequestBody(h.router, "POST", "/api/v1/admin/crypto/security/operation-intent", `{"action":"sweep"}`, "", nil, peer, host)
			require.GreaterOrEqual(t, w.Code, 400, w.Body.String())
			if test == "notifier offline" {
				require.Equal(t, 503, w.Code)
				require.Contains(t, w.Body.String(), "CRYPTO_NOTIFICATION_UNAVAILABLE")
			} else {
				require.Empty(t, push.messages)
			}
		})
	}
}
