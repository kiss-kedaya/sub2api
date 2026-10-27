package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mu     sync.Mutex
	config Config
}

func (s *memoryStore) Load(context.Context) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config, nil
}
func (s *memoryStore) Save(_ context.Context, c Config, expected string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config.Revision != expected {
		return ErrConfigConflict
	}
	s.config = c
	return nil
}
func testRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	ctx, cancel := context.WithCancel(r.Context())
	// Recorder requests need a cancellable context instead of Gin CloseNotifier.
	_ = cancel
	return r.WithContext(ctx)
}
func cacheFor(t *testing.T) *redis.Client {
	t.Helper()
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type mockCountry struct{}

func (mockCountry) Country(_ context.Context, ip string) string {
	if strings.HasPrefix(ip, "203.0.113.") || strings.HasPrefix(ip, "2001:db8:") {
		return "US"
	}
	return ""
}
func managerFor(t *testing.T, id string, cache *redis.Client, store *memoryStore) *Manager {
	t.Helper()
	r := Runtime{Secret: strings.Repeat("k", 32), Gateway: id != "", ID: id, Endpoint: "http://127.0.0.1", Region: "US", TrustedPrivateCIDRs: []string{"127.0.0.0/8"}}
	m, err := New(r, cache, store)
	require.NoError(t, err)
	m.ready = func(context.Context) error { return nil }
	m.geo = mockCountry{}
	m.reload(context.Background())
	t.Cleanup(m.Stop)
	return m
}
func registeredNode(t *testing.T, cache *redis.Client, store *memoryStore, handler gin.HandlerFunc) (*Manager, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	node := managerFor(t, "us-pod", cache, store)
	router := gin.New()
	router.Use(node.Ingress())
	router.GET("/internal/serverless/probe", node.ProbeHandler)
	router.POST("/v1/responses", handler)
	router.GET("/v1/responses", handler)
	router.GET("/api/bps-images/:token", node.ImageRoute, handler)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	node.runtime.Endpoint = srv.URL
	node.heartbeat(context.Background())
	return node, srv
}
func routeConfig(endpoint string) Config {
	return Config{Enabled: true, Established: true, Pods: []PodPolicy{{ID: "us-pod", Endpoint: endpoint, Enabled: true}},
		Regions: []Region{{Country: "US", PodIDs: []string{"us-pod"}}}}
}
func ingressRouter(m *Manager, key int64, ip string) http.Handler {
	r := gin.New()
	r.POST("/v1/responses", func(c *gin.Context) { m.Route(c, key, ip) }, func(c *gin.Context) { c.String(200, "primary") })
	r.GET("/v1/responses", func(c *gin.Context) { m.Route(c, key, ip) }, func(c *gin.Context) { c.String(200, "primary") })
	r.GET("/api/bps-images/:token", m.ImageRoute, func(c *gin.Context) { c.String(404, "not here") })
	return r
}
func request(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := testRequest("POST", path, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer fake-user-key")
	h.ServeHTTP(w, r)
	return w
}
func TestServerlessRegionRoutesAuthenticatedPayloadAndPinsKey(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	var hits atomic.Int32
	_, srv := registeredNode(t, cache, store, func(c *gin.Context) {
		hits.Add(1)
		require.Equal(t, "Bearer fake-user-key", c.GetHeader("Authorization"))
		require.Equal(t, "203.0.113.4", c.GetString("serverless_verified_ip"))
		c.String(200, "pod")
	})
	store.config = routeConfig(srv.URL)
	m := managerFor(t, "", cache, store)
	w := request(t, ingressRouter(m, 42, "203.0.113.4"), "/v1/responses")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "pod", w.Body.String())
	require.Equal(t, "us-pod", w.Header().Get("X-Sub2api-Pod"))
	// Same Key remains on the same node despite a changed region mapping.
	cfg := store.config
	cfg.Enabled = false
	require.NoError(t, m.Save(context.Background(), cfg))
	w = request(t, ingressRouter(m, 42, "203.0.113.4"), "/v1/responses")
	require.Equal(t, "pod", w.Body.String())
	require.Equal(t, int32(2), hits.Load())
	w = request(t, ingressRouter(m, 43, "198.51.100.1"), "/v1/responses")
	require.Equal(t, "primary", w.Body.String())
	stats, err := m.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, "1", stats["us-pod|US|region|requests"])
}
func TestServerlessUnavailableNewBindingFallsBackButExistingNeverMoves(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	node, srv := registeredNode(t, cache, store, func(c *gin.Context) { c.String(200, "pod") })
	store.config = routeConfig(srv.URL)
	m := managerFor(t, "", cache, store)
	require.Equal(t, "pod", request(t, ingressRouter(m, 1, "203.0.113.1"), "/v1/responses").Body.String())
	node.boot = randomID()
	// Simulate expiry of the old registration lease after node restart.
	require.NoError(t, cache.Del(context.Background(), prefix+"pod:us-pod").Err())
	node.heartbeat(context.Background())
	w := request(t, ingressRouter(m, 1, "203.0.113.1"), "/v1/responses")
	require.Equal(t, 503, w.Code)
	// An unreachable new node does not receive a new key's request.
	srv.Close()
	m.health = map[string]healthResult{}
	w = request(t, ingressRouter(m, 2, "203.0.113.1"), "/v1/responses")
	require.Equal(t, "primary", w.Body.String())
}
func TestServerlessPolicyRemovalDoesNotSilentlyMoveBinding(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	_, srv := registeredNode(t, cache, store, func(c *gin.Context) { c.String(200, "pod") })
	store.config = routeConfig(srv.URL)
	m := managerFor(t, "", cache, store)
	require.Equal(t, "pod", request(t, ingressRouter(m, 1, "203.0.113.1"), "/v1/responses").Body.String())
	require.NoError(t, m.Save(context.Background(), Config{}))
	require.Equal(t, 503, request(t, ingressRouter(m, 1, "203.0.113.1"), "/v1/responses").Code)
}
func TestServerlessRejectsForgedReplayedAndWrongAudienceTickets(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	node := managerFor(t, "us-pod", cache, store)
	r := gin.New()
	r.Use(node.Ingress())
	r.POST("/v1/responses", func(c *gin.Context) { c.Status(200) })
	req := testRequest("POST", "/v1/responses", nil)
	ticket := ticket{Node: "us-pod", Boot: node.boot, IP: "203.0.113.3", At: time.Now().Unix(), Nonce: randomID()}
	ticket.Body, _ = node.bodyDigest(req)
	ticket.Sig = node.proof(ticket, req)
	raw, _ := json.Marshal(ticket)
	for i, expected := range []int{200, 403} {
		w := httptest.NewRecorder()
		req := testRequest("POST", "/v1/responses", nil)
		req.Header.Set(forwardHeader, string(raw))
		r.ServeHTTP(w, req)
		require.Equal(t, expected, w.Code, "attempt %d", i)
	}
	ticket.Node = "other"
	raw, _ = json.Marshal(ticket)
	req = testRequest("POST", "/v1/responses", nil)
	req.Header.Set(forwardHeader, string(raw))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
}
func TestServerlessRegistrationRejectsDuplicateBootAndInvalidSignature(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	node := managerFor(t, "us-pod", cache, store)
	node.heartbeat(context.Background())
	other := managerFor(t, "us-pod", cache, store)
	other.heartbeat(context.Background())
	pods, err := node.Pods(context.Background())
	require.NoError(t, err)
	require.Len(t, pods, 1)
	require.Equal(t, node.boot, pods[0].Boot)
	require.NoError(t, cache.Set(context.Background(), prefix+"pod:us-pod", `{"pod":{"id":"us-pod","endpoint":"http://127.0.0.1"},"signature":"fake"}`, time.Minute).Err())
	pods, err = node.Pods(context.Background())
	require.NoError(t, err)
	require.Empty(t, pods)
}
func TestServerlessImageCapabilityReturnsToOwner(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	node, srv := registeredNode(t, cache, store, func(c *gin.Context) { c.String(200, "image") })
	store.config = routeConfig(srv.URL)
	m := managerFor(t, "", cache, store)
	raw := node.ImageOwnerURL("https://public.example/api/bps-images/capability")
	u, err := url.Parse(raw)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	ingressRouter(m, 1, "203.0.113.1").ServeHTTP(w, testRequest("GET", u.RequestURI(), nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "image", w.Body.String())
	u.Path = "/api/bps-images/forged"
	w = httptest.NewRecorder()
	ingressRouter(m, 1, "203.0.113.1").ServeHTTP(w, testRequest("GET", u.RequestURI(), nil))
	require.Equal(t, 404, w.Code)
}
func TestServerlessSSEFlushAndCancellationReachPod(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	cancelled := make(chan struct{})
	_, srv := registeredNode(t, cache, store, func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("data: first\n\n"))
		c.Writer.Flush()
		<-c.Request.Context().Done()
		close(cancelled)
	})
	store.config = routeConfig(srv.URL)
	m := managerFor(t, "", cache, store)
	edge := httptest.NewServer(ingressRouter(m, 1, "203.0.113.1"))
	defer edge.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", edge.URL+"/v1/responses", strings.NewReader("{}"))
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	require.NoError(t, err)
	data := make([]byte, len("data: first\n\n"))
	_, err = io.ReadFull(res.Body, data)
	require.NoError(t, err)
	require.Equal(t, "data: first\n\n", string(data))
	cancel()
	_ = res.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("Pod request not cancelled")
	}
}
func TestServerlessWebSocketBidirectionalForwarding(t *testing.T) {
	cache := cacheFor(t)
	store := &memoryStore{}
	_, srv := registeredNode(t, cache, store, func(c *gin.Context) {
		ws, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer func() { _ = ws.CloseNow() }()
		kind, msg, err := ws.Read(c.Request.Context())
		if err == nil {
			_ = ws.Write(c.Request.Context(), kind, msg)
		}
	})
	store.config = routeConfig(srv.URL)
	m := managerFor(t, "", cache, store)
	edge := httptest.NewServer(ingressRouter(m, 1, "203.0.113.1"))
	defer edge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(edge.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	defer func() { _ = ws.CloseNow() }()
	require.NoError(t, ws.Write(ctx, websocket.MessageText, []byte("hello")))
	_, msg, err := ws.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", string(msg))
}
func TestServerlessCountryAndEndpointValidation(t *testing.T) {
	c := routeConfig("http://127.0.0.1:9000")
	require.NoError(t, Validate(c))
	for _, origin := range []string{"http://169.254.169.254", "http://public.example", "https://user:pass@example.com", "https://example.com/a"} {
		require.Error(t, ValidateEndpoint(origin))
	}
	c.Regions[0].Country = "USA"
	require.Error(t, Validate(c))
	require.False(t, routePath(testRequest("POST", "/v1/images/generations", nil)))
	require.False(t, routePath(testRequest("GET", "/api/v1/admin/settings", nil)))
}
func TestServerlessStatsRecordHTTPErrorWithoutCredentials(t *testing.T) {
	m := managerFor(t, "", cacheFor(t), &memoryStore{})
	m.Record(context.Background(), "primary", "US", "default", 502)
	rows, err := m.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, map[string]string{"primary|US|default|requests": "1", "primary|US|default|errors": "1"}, rows)
}
func TestServerlessLifecycleStopsHeartbeat(t *testing.T) {
	m := managerFor(t, "us-pod", cacheFor(t), &memoryStore{})
	m.Start(func(context.Context) error { return fmt.Errorf("draining") })
	m.Stop()
	m.stop = nil
	select {
	case <-m.done:
	default:
		t.Fatal("agent did not stop")
	}
}
