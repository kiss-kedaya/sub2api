//go:build unit

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestGatewayIPSharingBusinessPathsOnly proves the wiring: probes and metrics
// never touch the counter, while three different accounts on a /v1 endpoint
// auto-add the source IP to the existing notice policy without rejecting the
// request.
func TestGatewayIPSharingBusinessPathsOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	settings := noticeTestSettings(t)
	detector := NewIPKeySharingDetector(settings, client)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseInt(c.GetHeader("X-Test-User"), 10, 64)
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: userID, UserID: userID, Key: fmt.Sprintf("sk-%d-secret", userID)})
		c.Next()
	})
	router.Use(GatewayIPSharing(settings, detector, nil))
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		router.GET(path, func(c *gin.Context) { c.Status(http.StatusOK) })
	}
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func(method, path string, userID int64) int {
		request := httptest.NewRequest(method, path, nil)
		request.RemoteAddr = "203.0.113.77:41234"
		request.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code
	}

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		for _, userID := range []int64{1, 2, 3, 4} {
			require.Equal(t, http.StatusOK, call(http.MethodGet, path, userID))
		}
	}
	require.False(t, server.Exists("gateway_ip_sharing:users:203.0.113.77"), "probe traffic must not be counted")
	require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)

	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 1))
	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 2))
	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 3))

	policy := settings.GatewayIPNoticeSnapshot()
	require.Len(t, policy.AutoEntries, 1)
	require.Equal(t, "203.0.113.77", policy.AutoEntries[0].IP)
	require.Equal(t, "auto", policy.AutoEntries[0].Source)
	require.Equal(t, 3, policy.AutoEntries[0].DistinctUsers)
	require.Equal(t, 3, len(policy.AutoEntries[0].KeyPrefixes))

	// The triggering request itself is never rejected: this middleware only
	// records. (The existing notice middleware decides enforcement.)
	require.Equal(t, http.StatusOK, call(http.MethodPost, "/v1/messages", 4))
	require.Len(t, settings.GatewayIPNoticeSnapshot().AutoEntries, 1, "repeat detections must not duplicate the entry")
}

// sharingKeyRepoStub is the minimal APIKeyRepository the risk control needs:
// GetByID to read the current status and Update to flip it. Everything else is
// deliberately absent from these tests.
type sharingKeyRepoStub struct {
	service.APIKeyRepository
	keys    map[int64]*service.APIKey
	updates int
}

func (r *sharingKeyRepoStub) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	key, ok := r.keys[id]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func (r *sharingKeyRepoStub) Update(_ context.Context, key *service.APIKey, _ service.APIKeyUpdateFields) error {
	clone := *key
	r.keys[key.ID] = &clone
	r.updates++
	return nil
}

func newSharingKeyService(t *testing.T, keys []*service.APIKey) (*service.APIKeyService, *sharingKeyRepoStub) {
	t.Helper()
	repo := &sharingKeyRepoStub{keys: map[int64]*service.APIKey{}}
	for _, key := range keys {
		clone := *key
		repo.keys[key.ID] = &clone
	}
	cfg := &config.Config{}
	cfg.Default.APIKeyPrefix = "sk-"
	return service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg), repo
}

func newSharingDetector(t *testing.T, settings *service.SettingService) *IPKeySharingDetector {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	detector := NewIPKeySharingDetector(settings, client)
	detector.now = func() time.Time { return sharingTestWindow }
	return detector
}

func newSharingRouter(
	t *testing.T,
	settings *service.SettingService,
	detector *IPKeySharingDetector,
	keysSvc *service.APIKeyService,
	keysByUser map[int64]*service.APIKey,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		userID, _ := strconv.ParseInt(c.GetHeader("X-Test-User"), 10, 64)
		key, ok := keysByUser[userID]
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(string(ContextKeyAPIKey), key)
		c.Next()
	})
	router.Use(GatewayIPSharing(settings, detector, keysSvc))
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

func callSharing(router *gin.Engine, userID int64) int {
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	request.RemoteAddr = "203.0.113.88:41234"
	request.Header.Set("X-Test-User", strconv.FormatInt(userID, 10))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code
}

func stubDatacenterClassifier(t *testing.T, datacenter bool) {
	t.Helper()
	previous := datacenterClassifier
	datacenterClassifier = func(context.Context, string) bool { return datacenter }
	t.Cleanup(func() { datacenterClassifier = previous })
}

func sharedEgressKey(userID int64, createdAt time.Time) *service.APIKey {
	return &service.APIKey{
		ID:        userID,
		UserID:    userID,
		Key:       fmt.Sprintf("sk-%d-secret", userID),
		Status:    service.StatusActive,
		CreatedAt: createdAt,
	}
}

// setKeyDisable flips the opt-in escalation switch on the stored policy, the
// same way the admin page does.
func setKeyDisable(t *testing.T, settings *service.SettingService, enabled bool) {
	t.Helper()
	policy, err := settings.GetGatewayIPNotice(context.Background())
	require.NoError(t, err)
	policy.KeyDisableEnabled = &enabled
	require.NoError(t, settings.SetGatewayIPNotice(context.Background(), policy))
}

// TestGatewayIPSharingDisablesKeysOnDatacenterSharedEgress pins the opt-in
// contract: with key_disable_enabled=true a hosting IP used by three distinct
// users adds the IP AND disables every observed key (status only, key rows
// never deleted).
func TestGatewayIPSharingDisablesKeysOnDatacenterSharedEgress(t *testing.T) {
	settings := noticeTestSettings(t)
	setKeyDisable(t, settings, true)
	detector := newSharingDetector(t, settings)
	keysByUser := map[int64]*service.APIKey{}
	var keys []*service.APIKey
	for _, userID := range []int64{11, 12, 13} {
		key := sharedEgressKey(userID, sharingTestKeyCreatedAt)
		keysByUser[userID] = key
		keys = append(keys, key)
	}
	keysSvc, repo := newSharingKeyService(t, keys)
	router := newSharingRouter(t, settings, detector, keysSvc, keysByUser)

	for _, userID := range []int64{11, 12, 13} {
		require.Equal(t, http.StatusOK, callSharing(router, userID))
	}

	require.Equal(t, 3, repo.updates, "every observed key is disabled exactly once")
	for id := range keysByUser {
		require.Equal(t, service.StatusAPIKeyInactive, repo.keys[id].Status, "key %d must be disabled", id)
	}
	// The address is deliberately NOT listed: several customers sharing one VPN
	// exit is normal, so only the shared credential is acted on.
	policy := settings.GatewayIPNoticeSnapshot()
	require.Empty(t, policy.AutoEntries, "key disable must not block the address")
	require.NotContains(t, policy.IPs, "203.0.113.77")
}

// TestGatewayIPSharingObserveOnlyLeavesKeysActive pins the product default:
// key_disable_enabled=false still appends the IP, but DisableKeysForRisk is
// never reached and every key keeps status=active.
func TestGatewayIPSharingObserveOnlyLeavesKeysActive(t *testing.T) {
	settings := noticeTestSettings(t)
	require.False(t, settings.GatewayIPNoticeSnapshot().KeyDisable(),
		"a policy without the switch must default to observe-only")
	detector := newSharingDetector(t, settings)
	keysByUser := map[int64]*service.APIKey{}
	var keys []*service.APIKey
	for _, userID := range []int64{41, 42, 43} {
		key := sharedEgressKey(userID, sharingTestKeyCreatedAt)
		keysByUser[userID] = key
		keys = append(keys, key)
	}
	keysSvc, repo := newSharingKeyService(t, keys)
	router := newSharingRouter(t, settings, detector, keysSvc, keysByUser)

	for _, userID := range []int64{41, 42, 43} {
		require.Equal(t, http.StatusOK, callSharing(router, userID))
	}

	require.Zero(t, repo.updates, "observe-only must not call DisableKeysForRisk")
	for id := range keysByUser {
		require.Equal(t, service.StatusActive, repo.keys[id].Status, "key %d must stay active", id)
	}
	policy := settings.GatewayIPNoticeSnapshot()
	require.Len(t, policy.AutoEntries, 1, "the IP is still appended in observe-only mode")
	require.Equal(t, "203.0.113.88", policy.AutoEntries[0].IP)
	require.Equal(t, 3, policy.AutoEntries[0].DistinctUsers)
	require.Len(t, policy.AutoEntries[0].KeyPrefixes, 3)
}

// TestGatewayIPSharingExplicitKeyDisableFalseLeavesKeysActive covers an
// operator who saved the switch as an explicit false instead of leaving the
// field unset.
func TestGatewayIPSharingExplicitKeyDisableFalseLeavesKeysActive(t *testing.T) {
	settings := noticeTestSettings(t)
	setKeyDisable(t, settings, false)
	detector := newSharingDetector(t, settings)
	keysByUser := map[int64]*service.APIKey{}
	var keys []*service.APIKey
	for _, userID := range []int64{51, 52, 53} {
		key := sharedEgressKey(userID, sharingTestKeyCreatedAt)
		keysByUser[userID] = key
		keys = append(keys, key)
	}
	keysSvc, repo := newSharingKeyService(t, keys)
	router := newSharingRouter(t, settings, detector, keysSvc, keysByUser)

	for _, userID := range []int64{51, 52, 53} {
		require.Equal(t, http.StatusOK, callSharing(router, userID))
	}

	require.Zero(t, repo.updates)
	for id := range keysByUser {
		require.Equal(t, service.StatusActive, repo.keys[id].Status)
	}
	require.Len(t, settings.GatewayIPNoticeSnapshot().AutoEntries, 1)
}

// TestGatewayIPSharingResidentialEgressTouchesNothing is the false-positive
// regression: four different users behind a home/office egress change nothing —
// no IP entry and no key write.
func TestGatewayIPSharingResidentialEgressTouchesNothing(t *testing.T) {
	stubDatacenterClassifier(t, false)

	settings := noticeTestSettings(t)
	detector := newSharingDetector(t, settings)
	keysByUser := map[int64]*service.APIKey{}
	var keys []*service.APIKey
	for _, userID := range []int64{21, 22, 23, 24} {
		key := sharedEgressKey(userID, sharingTestKeyCreatedAt)
		keysByUser[userID] = key
		keys = append(keys, key)
	}
	keysSvc, repo := newSharingKeyService(t, keys)
	router := newSharingRouter(t, settings, detector, keysSvc, keysByUser)

	for _, userID := range []int64{21, 22, 23, 24} {
		require.Equal(t, http.StatusOK, callSharing(router, userID))
	}

	require.Zero(t, repo.updates, "no key may be touched on a residential egress")
	for id := range keysByUser {
		require.Equal(t, service.StatusActive, repo.keys[id].Status)
	}
	require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
}

// TestGatewayIPSharingYoungKeysTouchNothing is the key-age regression: four
// one-day-old keys on a datacenter IP never reach the counter, so neither the
// IP list nor the key status changes.
func TestGatewayIPSharingYoungKeysTouchNothing(t *testing.T) {
	settings := noticeTestSettings(t)
	detector := newSharingDetector(t, settings)
	keysByUser := map[int64]*service.APIKey{}
	var keys []*service.APIKey
	for _, userID := range []int64{31, 32, 33, 34} {
		key := sharedEgressKey(userID, sharingTestWindow.Add(-24*time.Hour))
		keysByUser[userID] = key
		keys = append(keys, key)
	}
	keysSvc, repo := newSharingKeyService(t, keys)
	router := newSharingRouter(t, settings, detector, keysSvc, keysByUser)

	for _, userID := range []int64{31, 32, 33, 34} {
		require.Equal(t, http.StatusOK, callSharing(router, userID))
	}

	require.Zero(t, repo.updates, "a one-day-old key must not be disabled")
	for id := range keysByUser {
		require.Equal(t, service.StatusActive, repo.keys[id].Status)
	}
	require.Empty(t, settings.GatewayIPNoticeSnapshot().AutoEntries)
}
