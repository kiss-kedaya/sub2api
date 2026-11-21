package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyCreateReceiptRepo struct {
	service.APIKeyRepository
	created *service.APIKey
	calls   int
}

func (r *apiKeyCreateReceiptRepo) Create(_ context.Context, key *service.APIKey) error {
	r.calls++
	key.ID = 42
	r.created = key
	return nil
}

type apiKeyCreateReceiptJournal struct {
	*userMemoryIdempotencyRepoStub
	readErr error
}

func (r *apiKeyCreateReceiptJournal) GetByScopeAndKeyHash(ctx context.Context, scope, hash string) (*service.IdempotencyRecord, error) {
	if r.readErr != nil {
		return nil, r.readErr
	}
	return r.userMemoryIdempotencyRepoStub.GetByScopeAndKeyHash(ctx, scope, hash)
}

func TestAPIKeyCreateReplaysOnlyMaskedReceipt(t *testing.T) {
	journal := &apiKeyCreateReceiptJournal{userMemoryIdempotencyRepoStub: newUserMemoryIdempotencyRepoStub()}
	previous := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(journal, service.DefaultIdempotencyConfig()))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	keys := &apiKeyCreateReceiptRepo{}
	svc := service.NewAPIKeyService(keys, availableGroupsUserRepoStub{}, nil, nil, nil, nil, &config.Config{})
	handler := NewAPIKeyHandler(svc)
	router := gin.New()
	router.POST("/keys", withUserSubject(7), handler.Create)

	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader("{\"name\":\"disposable fixture\"}"))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "synthetic-create-receipt")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	data := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out struct{ Data map[string]any }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		return out.Data
	}

	first := call()
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "no-store", first.Header().Get("Cache-Control"))
	require.Empty(t, first.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, 1, keys.calls)
	require.NotNil(t, keys.created)
	require.NotEmpty(t, keys.created.Key)
	firstData := data(first)
	require.True(t, firstData["key"] == keys.created.Key, "only the first response returns the created credential")
	require.Equal(t, float64(42), firstData["id"])
	require.NotEmpty(t, firstData["key_masked"])
	require.Len(t, journal.data, 1)
	var record *service.IdempotencyRecord
	for _, record = range journal.data {
	}
	require.NotNil(t, record.ResponseBody)
	var stored map[string]any
	require.NoError(t, json.Unmarshal([]byte(*record.ResponseBody), &stored))
	require.NotContains(t, stored, "key")
	require.False(t, strings.Contains(*record.ResponseBody, keys.created.Key), "the retry journal must not retain the credential")

	assertMasked := func() {
		t.Helper()
		w := call()
		require.Equal(t, http.StatusOK, w.Code)
		require.Equal(t, "true", w.Header().Get("X-Idempotency-Replayed"))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		replay := data(w)
		require.NotContains(t, replay, "key")
		require.Equal(t, firstData["id"], replay["id"])
		require.Equal(t, firstData["key_masked"], replay["key_masked"])
		require.Equal(t, 1, keys.calls)
	}
	assertMasked()

	// The legacy shape is a fixture only; the current handler must omit
	// unknown fields from it and must not recreate the key.
	stored["key"] = "fixture-legacy-value-not-a-credential"
	raw, err := json.Marshal(stored)
	require.NoError(t, err)
	legacy := string(raw)
	record.ResponseBody = &legacy
	assertMasked()
	require.Equal(t, legacy, *record.ResponseBody, "old journal rows are not rewritten on replay")

	assertUnavailable := func() {
		t.Helper()
		w := call()
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		require.Equal(t, 1, keys.calls, "failed recovery must not create a new key")
		require.False(t, strings.Contains(w.Body.String(), keys.created.Key), "failed recovery must not return any credentials")
	}

	stored["user_id"] = 8
	raw, err = json.Marshal(stored)
	require.NoError(t, err)
	wrongOwner := string(raw)
	record.ResponseBody = &wrongOwner
	assertUnavailable()

	invalid := "{"
	record.ResponseBody = &invalid
	assertUnavailable()

	record.ResponseBody = &legacy
	journal.readErr = errors.New("fixture: retry journal unavailable")
	assertUnavailable()
}
