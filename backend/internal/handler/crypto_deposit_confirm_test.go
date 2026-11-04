package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const depositConfirmPath = "/api/v1/admin/crypto/deposits/confirm"

type depositConfirmCtxKey struct{}

type depositConfirmerFake struct {
	mu       sync.Mutex
	calls    int
	adminID  int64
	ids      []int64
	note     string
	ctxErr   error
	ctxValue any
	summary  *cryptopay.DepositConfirmSummary
	err      error
}

func (f *depositConfirmerFake) ConfirmDeposits(ctx context.Context, adminID int64, ids []int64, note string) (*cryptopay.DepositConfirmSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.adminID, f.ids, f.note = adminID, append([]int64(nil), ids...), note
	f.ctxErr, f.ctxValue = ctx.Err(), ctx.Value(depositConfirmCtxKey{})
	return f.summary, f.err
}

func (f *depositConfirmerFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func orderID(n int64) *int64 { return &n }

func okSummary() *cryptopay.DepositConfirmSummary {
	return &cryptopay.DepositConfirmSummary{Credited: 1, Failed: 1, Results: []cryptopay.DepositConfirmResult{
		{ID: 302, Status: cryptopay.DepositConfirmCredited, OrderID: orderID(123), Message: "已入账。"},
		{ID: 321, Status: cryptopay.DepositConfirmRejected, Message: "链上这笔交易已回滚（receipt status=0），不能入账。"},
	}}
}

func depositConfirmRouter(h *CryptoHandler, authenticated bool, extra ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if authenticated {
			c.Set("auth_method", service.AuditAuthMethodJWT)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
			c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		}
		c.Next()
	})
	router.Use(extra...)
	router.POST(depositConfirmPath, h.AdminConfirmDeposits)
	return router
}

func postDepositConfirm(router http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, depositConfirmPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func depositConfirmJSON(t *testing.T, v any) string {
	t.Helper()
	encoded, err := json.Marshal(v)
	require.NoError(t, err)
	return string(encoded)
}

func TestAdminConfirmDepositsReturnsTheAgreedShapeAndPassesNormalizedInput(t *testing.T) {
	fake := &depositConfirmerFake{summary: okSummary()}
	router := depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true)

	w := postDepositConfirm(router, `{"ids":[302,321,302,5,321],"note":"  checked against the explorer  "}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Header().Get("Cache-Control"), "no-store")

	body := w.Body.String()
	require.EqualValues(t, 1, gjson.Get(body, "data.credited").Int())
	require.EqualValues(t, 1, gjson.Get(body, "data.failed").Int())
	require.Len(t, gjson.Get(body, "data.results").Array(), 2)
	require.EqualValues(t, 302, gjson.Get(body, "data.results.0.id").Int())
	require.Equal(t, "credited", gjson.Get(body, "data.results.0.status").String())
	require.EqualValues(t, 123, gjson.Get(body, "data.results.0.order_id").Int())
	require.NotEmpty(t, gjson.Get(body, "data.results.0.message").String())
	require.Equal(t, "rejected", gjson.Get(body, "data.results.1.status").String())
	require.False(t, gjson.Get(body, "data.results.1.order_id").Exists(), "no order means no order_id key")
	require.False(t, gjson.Get(body, "code").Exists(), "the agreed body is {data:{...}} with no envelope")

	require.Equal(t, 1, fake.callCount())
	require.EqualValues(t, 41, fake.adminID, "the operator comes from the authenticated session, never the body")
	require.Equal(t, []int64{302, 321, 5}, fake.ids, "duplicates removed, first-seen order kept")
	require.Equal(t, "checked against the explorer", fake.note)
}

func TestAdminConfirmDepositsRefusesMalformedRequestsWithoutCallingTheService(t *testing.T) {
	distinct := make([]int64, 51)
	for i := range distinct {
		distinct[i] = int64(i + 1)
	}
	for name, body := range map[string]string{
		"empty body":             ``,
		"null":                   `null`,
		"array body":             `[1,2]`,
		"no ids":                 `{}`,
		"empty ids":              `{"ids":[]}`,
		"null ids":               `{"ids":null}`,
		"zero id":                `{"ids":[0]}`,
		"negative id":            `{"ids":[-1]}`,
		"one bad id among good":  `{"ids":[1,2,0]}`,
		"string id":              `{"ids":["1"]}`,
		"fractional id":          `{"ids":[1.5]}`,
		"exponent id":            `{"ids":[1e3]}`,
		"overflow id":            `{"ids":[9223372036854775808]}`,
		"more than 50 distinct":  depositConfirmJSON(t, map[string]any{"ids": distinct}),
		"skip-verification flag": `{"ids":[1],"skip_verify":true}`,
		"force flag":             `{"ids":[1],"force":true}`,
		"unknown field":          `{"ids":[1],"chain":"bsc"}`,
		"trailing json":          `{"ids":[1]} {"ids":[2]}`,
		"trailing garbage":       `{"ids":[1]}x`,
		"note over 200 runes":    depositConfirmJSON(t, map[string]any{"ids": []int64{1}, "note": strings.Repeat("账", 201)}),
		"note is not a string":   `{"ids":[1],"note":5}`,
		"body over the limit":    depositConfirmJSON(t, map[string]any{"ids": []int64{1}, "note": strings.Repeat("x", 9000)}),
	} {
		t.Run(name, func(t *testing.T) {
			fake := &depositConfirmerFake{summary: okSummary()}
			w := postDepositConfirm(depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true), body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.Zero(t, fake.callCount())
		})
	}
}

func TestAdminConfirmDepositsNoteLimitCountsCharactersNotBytes(t *testing.T) {
	cjk200 := strings.Repeat("账", 200) // 600 bytes
	for name, tc := range map[string]struct{ note, want string }{
		"200 CJK characters":    {cjk200, cjk200},
		"200 padded with space": {"  " + strings.Repeat("a", 200) + "\t", strings.Repeat("a", 200)},
		"absent":                {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &depositConfirmerFake{summary: okSummary()}
			w := postDepositConfirm(depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true),
				depositConfirmJSON(t, map[string]any{"ids": []int64{1}, "note": tc.note}))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, tc.want, fake.note)
		})
	}
	fake := &depositConfirmerFake{summary: okSummary()}
	w := postDepositConfirm(depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true), `{"ids":[1]}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, fake.note)
}

func TestAdminConfirmDepositsRequiresAnAuthenticatedOperatorAndAConfiguredService(t *testing.T) {
	fake := &depositConfirmerFake{summary: okSummary()}
	w := postDepositConfirm(depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, false), `{"ids":[1]}`)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Zero(t, fake.callCount())

	w = postDepositConfirm(depositConfirmRouter(&CryptoHandler{}, true), `{"ids":[1]}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
}

func TestAdminConfirmDepositsMapsServiceErrorsWithoutEchoingTheirText(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"invalid":               {cryptopay.ErrDepositConfirmInvalid, http.StatusBadRequest},
		"security lock":         {cryptopay.ErrDepositConfirmSecurityLocked, http.StatusConflict},
		"wrapped security lock": {fmt.Errorf("gate: %w", cryptopay.ErrDepositConfirmSecurityLocked), http.StatusConflict},
		"read-only instance":    {cryptopay.ErrDepositConfirmReadOnly, http.StatusForbidden},
		"unavailable":           {cryptopay.ErrDepositConfirmUnavailable, http.StatusServiceUnavailable},
		"unknown failure": {errors.New("pq: password authentication failed for user secret-db-user at 10.9.8.7:5432"),
			http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &depositConfirmerFake{err: tc.err}
			w := postDepositConfirm(depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true), `{"ids":[1]}`)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "secret-db-user")
			require.NotContains(t, w.Body.String(), "10.9.8.7")
			require.False(t, gjson.Get(w.Body.String(), "data.results").Exists())
		})
	}
	fake := &depositConfirmerFake{}
	w := postDepositConfirm(depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true), `{"ids":[1]}`)
	require.Equal(t, http.StatusServiceUnavailable, w.Code, "a nil summary is never reported as success")
}

// Closing the tab must not leave a half-processed batch: the handler hands the
// service a context that no longer follows the request's cancellation, while
// still carrying the request's values.
func TestAdminConfirmDepositsServiceContextSurvivesRequestCancellation(t *testing.T) {
	fake := &depositConfirmerFake{summary: okSummary()}
	router := depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), depositConfirmCtxKey{}, "trace-9"))
	cancel()
	req := httptest.NewRequest(http.MethodPost, depositConfirmPath, strings.NewReader(`{"ids":[1]}`)).WithContext(ctx)
	router.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, 1, fake.callCount())
	require.NoError(t, fake.ctxErr)
	require.Equal(t, "trace-9", fake.ctxValue)
}

func TestAdminConfirmDepositsAuditRecordsActionActorOutcomeAndNoCredential(t *testing.T) {
	for name, tc := range map[string]struct {
		summary *cryptopay.DepositConfirmSummary
		result  string
	}{
		"everything credited": {&cryptopay.DepositConfirmSummary{Credited: 2, Results: []cryptopay.DepositConfirmResult{
			{ID: 302, Status: cryptopay.DepositConfirmCredited}, {ID: 321, Status: cryptopay.DepositConfirmCredited}}}, "success"},
		"only already credited": {&cryptopay.DepositConfirmSummary{Results: []cryptopay.DepositConfirmResult{
			{ID: 302, Status: cryptopay.DepositConfirmAlreadyCredited}, {ID: 321, Status: cryptopay.DepositConfirmAlreadyCredited}}}, "success"},
		"mixed":      {okSummary(), "partial"},
		"all failed": {&cryptopay.DepositConfirmSummary{Failed: 2, Results: []cryptopay.DepositConfirmResult{{ID: 302, Status: cryptopay.DepositConfirmError}, {ID: 321, Status: cryptopay.DepositConfirmRejected}}}, "failed"},
	} {
		t.Run(name, func(t *testing.T) {
			capture := &cryptoAuditCapture{}
			audit := service.NewAuditLogService(capture, nil)
			audit.Start()
			fake := &depositConfirmerFake{summary: tc.summary}
			router := depositConfirmRouter(&CryptoHandler{depositConfirmer: fake}, true,
				gin.HandlerFunc(middleware.NewAuditLogMiddleware(audit)))
			req := httptest.NewRequest(http.MethodPost, depositConfirmPath, strings.NewReader(`{"ids":[302,321],"note":"hand check"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Crypto-TOTP", "654321")
			router.ServeHTTP(httptest.NewRecorder(), req)
			audit.Stop()

			require.Len(t, capture.records, 1)
			record := capture.records[0]
			require.Equal(t, "admin.crypto.deposit.manual_confirm", record.Action)
			require.NotNil(t, record.ActorUserID)
			require.EqualValues(t, 41, *record.ActorUserID)
			require.Equal(t, http.StatusOK, record.StatusCode)
			require.Equal(t, tc.result, record.Extra["result"])
			require.EqualValues(t, 2, record.Extra["requested_count"])
			require.Contains(t, record.RequestBody, "302")
			require.Contains(t, record.RequestBody, "hand check")
			encoded, err := json.Marshal(record)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "654321", "the one-time code must not reach the audit row")
		})
	}
}
