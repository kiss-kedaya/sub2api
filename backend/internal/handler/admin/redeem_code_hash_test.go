package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const redeemHashTestPlaintext = "AAAABBBBCCCCDDDDEEEEFFFF00001111"

func newRedeemHashTestRouter() (*gin.Engine, *stubAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()
	h := NewRedeemHandler(adminSvc, nil)
	router.GET("/api/v1/admin/redeem-codes", h.List)
	router.GET("/api/v1/admin/redeem-codes/:id", h.GetByID)
	router.GET("/api/v1/admin/redeem-codes/export", h.Export)
	router.POST("/api/v1/admin/redeem-codes/generate", h.Generate)
	return router, adminSvc
}

func seedRedeemHashTestCodes(adminSvc *stubAdminService) {
	// 模拟真实服务返回：生成路径内存对象带明文 + 哈希 + 前缀；
	// 列表/详情路径（这里复用同一个 stub）故意给一行「尚未回填的历史明文行」，
	// 用来证明 DTO 层不会因为仓储形态而把明文透出去。
	adminSvc.redeems = []service.RedeemCode{
		{
			ID:         7,
			Code:       redeemHashTestPlaintext,
			CodeHash:   service.HashRedeemCode(redeemHashTestPlaintext),
			CodePrefix: redeemHashTestPlaintext[:8],
			Type:       service.RedeemTypeBalance,
			Value:      10,
			Status:     service.StatusUnused,
		},
	}
}

func TestRedeemGenerateReturnsPlaintextOnceAndReplaysMasked(t *testing.T) {
	journal := newMemoryIdempotencyRepoStub()
	previous := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(journal, service.DefaultIdempotencyConfig()))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })

	router, adminSvc := newRedeemHashTestRouter()
	seedRedeemHashTestCodes(adminSvc)

	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redeem-codes/generate",
			strings.NewReader(`{"count":1,"type":"balance","value":10}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "redeem-generate-once")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	data := func(w *httptest.ResponseRecorder) []map[string]any {
		t.Helper()
		var out struct {
			Data []map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out.Data
	}

	first := call()
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "no-store", first.Header().Get("Cache-Control"))
	require.Empty(t, first.Header().Get("X-Idempotency-Replayed"))
	firstData := data(first)
	require.Len(t, firstData, 1)
	require.Equal(t, redeemHashTestPlaintext, firstData[0]["code"], "the first response is the only plaintext entry point")
	require.Equal(t, redeemHashTestPlaintext[:8], firstData[0]["code_prefix"])

	// 幂等 journal 里只能有掩码：重放 / 报错日志都不能把明文带回去。
	require.Len(t, journal.data, 1)
	var record *service.IdempotencyRecord
	for _, rec := range journal.data {
		record = rec
	}
	require.NotNil(t, record.ResponseBody)
	require.NotContains(t, *record.ResponseBody, redeemHashTestPlaintext)
	require.Contains(t, *record.ResponseBody, "AAAA****")

	assertMaskedReplay := func() {
		t.Helper()
		w := call()
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "true", w.Header().Get("X-Idempotency-Replayed"))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.NotContains(t, w.Body.String(), redeemHashTestPlaintext)
		replay := data(w)
		require.Len(t, replay, 1)
		require.Equal(t, "AAAA****", replay[0]["code"])
	}
	assertMaskedReplay()

	// 旧版本 journal 里可能存过明文（当时整个响应被原样保存）。重放必须重新掩码，
	// 不能因为「是历史数据」就把可兑换卡密转发出去。
	var stored []map[string]any
	require.NoError(t, json.Unmarshal([]byte(*record.ResponseBody), &stored))
	require.NotEmpty(t, stored)
	legacyRaw, err := json.Marshal([]map[string]any{{
		"id":          float64(7),
		"code":        redeemHashTestPlaintext,
		"type":        "balance",
		"value":       float64(10),
		"status":      "unused",
		"created_at":  "2026-01-01T00:00:00Z",
		"code_prefix": redeemHashTestPlaintext[:8],
	}})
	require.NoError(t, err)
	legacy := string(legacyRaw)
	record.ResponseBody = &legacy
	w := call()
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), redeemHashTestPlaintext)
	replay := data(w)
	require.Len(t, replay, 1)
	require.Equal(t, "AAAA****", replay[0]["code"])
}

func TestRedeemListAndDetailNeverReturnPlaintext(t *testing.T) {
	router, adminSvc := newRedeemHashTestRouter()
	seedRedeemHashTestCodes(adminSvc)

	list := httptest.NewRecorder()
	router.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/admin/redeem-codes", nil))
	require.Equal(t, http.StatusOK, list.Code)
	require.NotContains(t, list.Body.String(), redeemHashTestPlaintext)
	require.Contains(t, list.Body.String(), "AAAA****")
	require.Contains(t, list.Body.String(), "AAAABBBB")

	// 详情不存在单独的明文入口（GET 类「查看卡密」接口在 #481 之后就不再返回明文）。
	detail := httptest.NewRecorder()
	router.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/api/v1/admin/redeem-codes/5", nil))
	require.Equal(t, http.StatusOK, detail.Code)
	require.NotContains(t, detail.Body.String(), "R-TEST", "short codes must not be echoed in full")
	require.Contains(t, detail.Body.String(), "****")
}

func TestRedeemExportContainsOnlyMaskAndMetadata(t *testing.T) {
	router, adminSvc := newRedeemHashTestRouter()
	seedRedeemHashTestCodes(adminSvc)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/redeem-codes/export", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	require.NotContains(t, body, redeemHashTestPlaintext)
	// 表头明确区分掩码与前缀；不再有可直接兑换的 code 列。
	require.Contains(t, body, "code_mask")
	require.Contains(t, body, "code_prefix")
	require.NotContains(t, body, "id,code,type")
	require.Contains(t, body, "AAAA****")
	require.Contains(t, body, "AAAABBBB")
}
