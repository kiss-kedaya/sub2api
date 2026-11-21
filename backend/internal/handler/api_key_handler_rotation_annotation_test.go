package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// apiKeyRotationListRepo serves a fixed page so the handler test can assert the
// real GET /api/v1/keys JSON shape without a database.
type apiKeyRotationListRepo struct {
	service.APIKeyRepository
	keys []service.APIKey
}

func (r *apiKeyRotationListRepo) ListByUserID(
	_ context.Context,
	_ int64,
	params pagination.PaginationParams,
	_ service.APIKeyListFilters,
) ([]service.APIKey, *pagination.PaginationResult, error) {
	return r.keys, &pagination.PaginationResult{
		Total:    int64(len(r.keys)),
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    1,
	}, nil
}

func TestAPIKeyListExposesNeedsRotation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cutoff := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-24 * time.Hour)
	cfg := &config.Config{Security: config.SecurityConfig{
		KeyRotationGateEnabled: true,
		KeyRotationCutoff:      cutoff.Format(time.RFC3339),
	}}
	repo := &apiKeyRotationListRepo{keys: []service.APIKey{
		{ID: 1, UserID: 7, Status: service.StatusAPIKeyActive, KeyPrefix: "sk-old", CreatedAt: old},
		{ID: 2, UserID: 7, Status: service.StatusAPIKeyActive, KeyPrefix: "sk-new", CreatedAt: cutoff.Add(time.Hour)},
		{ID: 3, UserID: 7, Status: service.StatusAPIKeyActive, KeyPrefix: "sk-allow", CreatedAt: old, IPWhitelist: []string{"203.0.113.7"}},
		{ID: 4, UserID: 7, Status: service.StatusAPIKeyInactive, KeyPrefix: "sk-off", CreatedAt: old},
	}}
	router := gin.New()
	router.GET("/keys", withUserSubject(7), NewAPIKeyHandler(service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)).List)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/keys", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var out struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out.Data.Items, 4)

	require.Equal(t, true, out.Data.Items[0]["needs_rotation"], "an old active key without an allowlist must be flagged")
	reasons, ok := out.Data.Items[0]["rotation_reasons"].([]any)
	require.True(t, ok, "pending keys must carry reason categories")
	require.ElementsMatch(t, []any{
		service.KeyRotationKeyReasonCreatedBeforeCutoff,
		service.KeyRotationKeyReasonNoIPAllowlist,
	}, reasons)

	require.Equal(t, false, out.Data.Items[1]["needs_rotation"])
	require.NotContains(t, out.Data.Items[1], "rotation_reasons")
	require.Equal(t, false, out.Data.Items[2]["needs_rotation"])
	require.Equal(t, false, out.Data.Items[3]["needs_rotation"], "inactive keys do not participate")
}
