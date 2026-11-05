package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func upstreamTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestUpstreamChannelClientLogin(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/login", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "ops@example.com", payload["email"])
		require.Equal(t, "secret", payload["password"])
		writeJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"data": map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"expires_in":    3600,
			},
		})
	})

	client := NewUpstreamChannelClient()
	session, err := client.Login(context.Background(), server.URL, "ops@example.com", "secret", "", "")
	require.NoError(t, err)
	require.Equal(t, "access-1", session.AccessToken)
	require.Equal(t, "refresh-1", session.RefreshToken)
	require.False(t, session.Expired(client.clock()))
}

func TestUpstreamChannelClientLoginRequires2FA(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"requires_2fa": true}})
	})

	client := NewUpstreamChannelClient()
	_, err := client.Login(context.Background(), server.URL, "ops@example.com", "secret", "", "")
	require.ErrorIs(t, err, ErrUpstreamChannelRequires2FA)
}

func TestUpstreamChannelClientLoginErrorEnvelope(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 1001, "message": "invalid credentials"})
	})

	client := NewUpstreamChannelClient()
	_, err := client.Login(context.Background(), server.URL, "ops@example.com", "wrong", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid credentials")
}

func TestUpstreamChannelClientHTTPErrorSurfacesMessage(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 401, "message": "token expired"})
	})

	client := NewUpstreamChannelClient()
	_, err := client.Balance(context.Background(), server.URL, "", &UpstreamChannelSession{AccessToken: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

func TestUpstreamChannelClientBalanceFallsBackToProfile(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/me":
			writeJSON(w, http.StatusNotFound, map[string]any{"message": "not found"})
		case "/api/v1/user/profile":
			writeJSON(w, http.StatusOK, map[string]any{"balance": 42.5})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := NewUpstreamChannelClient()
	balance, err := client.Balance(context.Background(), server.URL, "", &UpstreamChannelSession{AccessToken: "x"})
	require.NoError(t, err)
	require.Equal(t, 42.5, balance)
}

func TestUpstreamChannelClientAnnouncements(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/announcements", r.URL.Path)
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		writeJSON(w, http.StatusOK, []map[string]any{
			{"id": 1, "title": "维护", "content": "今晚维护", "notify_mode": "popup", "starts_at": "2026-11-05T08:00:00Z"},
			{"id": 0, "title": "无 id", "content": "哈希身份", "created_at": "2026-11-04T08:00:00Z"},
		})
	})

	client := NewUpstreamChannelClient()
	items, err := client.Announcements(context.Background(), server.URL, "", &UpstreamChannelSession{AccessToken: "token"})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "1", items[0].SourceKey)
	require.True(t, strings.HasPrefix(items[1].SourceKey, "h:"))
	require.NotNil(t, items[0].PublishedAt)
}

func TestUpstreamChannelClientListAPIKeys(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/keys":
			require.Equal(t, "1", r.URL.Query().Get("page"))
			require.Equal(t, "inactive", r.URL.Query().Get("status"))
			writeJSON(w, http.StatusOK, map[string]any{
				"items": []map[string]any{{
					"id": 11, "name": "team", "status": "inactive", "group_id": 5,
					"quota": 100, "quota_used": 20, "ip_whitelist": []string{"10.0.0.1"},
				}},
				"total": 1, "page": 1, "page_size": 20, "pages": 1,
			})
		case "/api/v1/groups/available":
			writeJSON(w, http.StatusOK, []map[string]any{{"id": 5, "name": "vip", "rate_multiplier": 0.8}})
		case "/api/v1/groups/rates":
			writeJSON(w, http.StatusOK, map[string]float64{"5": 0.75})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := NewUpstreamChannelClient()
	page, err := client.ListAPIKeys(context.Background(), server.URL, "", &UpstreamChannelSession{AccessToken: "token"}, UpstreamAPIKeyQuery{
		Page: 1, PageSize: 20, Status: "disabled",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, page.Items, 1)
	require.Equal(t, "disabled", page.Items[0].Status, "upstream inactive maps to our disabled")
	require.Equal(t, "vip", page.Items[0].GroupName)
	require.Equal(t, 0.75, page.Items[0].GroupRatio, "rate override wins")
}

func TestUpstreamChannelClientRechargeInfoAndOrder(t *testing.T) {
	var orderPayload map[string]any
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/payment/checkout-info":
			writeJSON(w, http.StatusOK, map[string]any{
				"global_min": 5, "global_max": 500,
				"help_text": "充值到上游账户",
				"methods": map[string]any{
					"alipay": map[string]any{"single_min": 10, "single_max": 400, "available": true},
				},
				"plans": []map[string]any{{
					"id": 7, "name": "月付", "description": "标准", "price": 99,
					"validity_days": 1, "validity_unit": "month", "group_name": "vip",
					"features": []string{"高并发"},
				}},
			})
		case "/api/v1/payment/orders":
			body, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(body, &orderPayload))
			writeJSON(w, http.StatusOK, map[string]any{"pay_url": "https://pay.example.com/x", "expires_at": "2026-11-05T10:00:00Z"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	client := NewUpstreamChannelClient()
	session := &UpstreamChannelSession{AccessToken: "token"}

	info, err := client.RechargeInfo(context.Background(), server.URL, "", session)
	require.NoError(t, err)
	require.Equal(t, 5.0, info.MinAmount)
	require.Len(t, info.Methods, 1)
	require.Equal(t, "alipay", info.Methods[0].Type)

	plans, err := client.SubscriptionInfo(context.Background(), server.URL, "", session)
	require.NoError(t, err)
	require.Len(t, plans.Plans, 1)
	require.Equal(t, "7", plans.Plans[0].ID)
	require.Equal(t, "1 个月", plans.Plans[0].Validity)

	launch, err := client.CreateRecharge(context.Background(), server.URL, "", session, UpstreamRechargeRequest{
		Amount: 12.345, PaymentMethod: "alipay",
	})
	require.NoError(t, err)
	require.Equal(t, "https://pay.example.com/x", launch.PayURL)
	require.Equal(t, "balance", orderPayload["order_type"])
	require.Equal(t, 12.35, orderPayload["amount"])

	_, err = client.CreateSubscription(context.Background(), server.URL, "", session, UpstreamSubscriptionRequest{
		PlanID: "7", PaymentMethod: "alipay",
	})
	require.NoError(t, err)
	require.Equal(t, "subscription", orderPayload["order_type"])
	require.Equal(t, float64(7), orderPayload["plan_id"])
}

func TestUpstreamChannelClientRejectsBadPaymentInput(t *testing.T) {
	client := NewUpstreamChannelClient()
	session := &UpstreamChannelSession{AccessToken: "token"}

	_, err := client.CreateRecharge(context.Background(), "https://example.com", "", session, UpstreamRechargeRequest{Amount: 0, PaymentMethod: "alipay"})
	require.Error(t, err)

	_, err = client.CreateRecharge(context.Background(), "https://example.com", "", session, UpstreamRechargeRequest{Amount: 10, PaymentMethod: "card"})
	require.Error(t, err)

	_, err = client.CreateSubscription(context.Background(), "https://example.com", "", session, UpstreamSubscriptionRequest{PlanID: "abc", PaymentMethod: "alipay"})
	require.Error(t, err)
}

func TestUpstreamChannelClientRedeem(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/redeem", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "CODE-1", payload["code"])
		writeJSON(w, http.StatusOK, map[string]any{
			"message": "兑换成功", "value": 20, "new_balance": 120.5,
		})
	})

	client := NewUpstreamChannelClient()
	result, err := client.Redeem(context.Background(), server.URL, "", &UpstreamChannelSession{AccessToken: "token"}, " CODE-1 ")
	require.NoError(t, err)
	require.Equal(t, "兑换成功", result.Message)
	require.Equal(t, "balance", result.Type)
	require.NotNil(t, result.NewBalance)
	require.Equal(t, 120.5, *result.NewBalance)

	_, err = client.Redeem(context.Background(), server.URL, "", &UpstreamChannelSession{AccessToken: "token"}, "  ")
	require.Error(t, err)
}

func TestUpstreamChannelClientPublicSettings(t *testing.T) {
	server := upstreamTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/settings/public", r.URL.Path)
		writeJSON(w, http.StatusOK, map[string]any{"turnstile_enabled": true, "turnstile_site_key": "site-key"})
	})

	client := NewUpstreamChannelClient()
	enabled, key, err := client.PublicSettings(context.Background(), server.URL, "")
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, "site-key", key)
}
