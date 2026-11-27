//go:build unit

package routes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type routesMemoryCaptureStore struct {
	mu      sync.Mutex
	tasks   map[string][]byte
	records map[string]requestcapture.Record
}

func newRoutesMemoryCaptureStore() *routesMemoryCaptureStore {
	return &routesMemoryCaptureStore{
		tasks:   map[string][]byte{},
		records: map[string]requestcapture.Record{},
	}
}

func (s *routesMemoryCaptureStore) SaveTask(_ context.Context, t *requestcapture.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t != nil {
		b, _ := json.Marshal(t)
		s.tasks[t.ID] = b
	}
	return nil
}

func (s *routesMemoryCaptureStore) Task(_ context.Context, instance, id string) (*requestcapture.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.tasks[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var t requestcapture.Task
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	if t.InstanceID != instance {
		return nil, sql.ErrNoRows
	}
	return &t, nil
}

func (s *routesMemoryCaptureStore) Tasks(_ context.Context, instance string, limit, offset int) ([]requestcapture.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []requestcapture.Task{}
	for _, b := range s.tasks {
		var t requestcapture.Task
		if json.Unmarshal(b, &t) == nil && t.InstanceID == instance {
			out = append(out, t)
		}
	}
	if offset >= len(out) {
		return []requestcapture.Task{}, nil
	}
	end := len(out)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return out[offset:end], nil
}

func (s *routesMemoryCaptureStore) SaveRecord(_ context.Context, r *requestcapture.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r != nil {
		s.records[r.ID] = *r
	}
	return nil
}

func (s *routesMemoryCaptureStore) Records(_ context.Context, _, _ string, _ bool, _, _ int) ([]requestcapture.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]requestcapture.Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	return out, nil
}

func (s *routesMemoryCaptureStore) Record(_ context.Context, _, id string) (*requestcapture.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return &r, nil
}

func (s *routesMemoryCaptureStore) DeleteRecord(_ context.Context, _, _ string) error { return nil }
func (s *routesMemoryCaptureStore) DeleteTask(_ context.Context, _, _ string) error   { return nil }

type routesAccountRepoStub struct {
	service.AccountRepository
	account service.Account
}

func (s *routesAccountRepoStub) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	if s.account.Platform != platform {
		return nil, nil
	}
	return []service.Account{s.account}, nil
}

func (s *routesAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]service.Account, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

func (s *routesAccountRepoStub) ListSchedulableByGroupID(_ context.Context, _ int64) ([]service.Account, error) {
	return []service.Account{s.account}, nil
}

func (s *routesAccountRepoStub) GetByID(_ context.Context, id int64) (*service.Account, error) {
	if s.account.ID != id {
		return nil, nil
	}
	acc := s.account
	return &acc, nil
}

type routesNonRetainingUpstream struct {
	fn func(req *http.Request) (*http.Response, error)
}

func (u *routesNonRetainingUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.fn(req)
}

func (u *routesNonRetainingUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.fn(req)
}

type routesSSEResponseWriter struct {
	header http.Header
	mu     sync.Mutex
	code   int
	buf    bytes.Buffer
	notify chan struct{}
}

func newRoutesSSEResponseWriter() *routesSSEResponseWriter {
	return &routesSSEResponseWriter{
		header: make(http.Header),
		code:   http.StatusOK,
		notify: make(chan struct{}, 64),
	}
}

func (w *routesSSEResponseWriter) Header() http.Header { return w.header }
func (w *routesSSEResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	w.code = code
	w.mu.Unlock()
}
func (w *routesSSEResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return n, err
}
func (w *routesSSEResponseWriter) Flush() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}
func (w *routesSSEResponseWriter) Snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
func (w *routesSSEResponseWriter) StatusCode() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.code
}

func waitRoutesOutput(t *testing.T, w *routesSSEResponseWriter, substr string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(w.Snapshot(), substr) {
			return
		}
		select {
		case <-w.notify:
		case <-deadline:
			t.Fatalf("timed out waiting for %q; got:\n%s", substr, w.Snapshot())
		}
	}
}

func TestMidStreamBodyRelease_FullMiddlewareHandlerServiceChain(t *testing.T) {
	gin.SetMode(gin.TestMode)

	captureStore := newRoutesMemoryCaptureStore()
	captureDir := t.TempDir()
	captureMgr, err := requestcapture.New(captureStore, captureDir, requestcapture.Config{
		Enabled:              true,
		QuotaMiB:             64,
		RetentionDays:        1,
		ID1DiagnosticEnabled: true,
		DiagnosticUserIDs:    []int64{1751},
	})
	require.NoError(t, err)
	defer captureMgr.Close()

	var taskID string
	require.Eventually(t, func() bool {
		tasks, tErr := captureMgr.Tasks(t.Context(), 10, 0)
		if tErr != nil || len(tasks) == 0 || tasks[0].Status != "running" {
			return false
		}
		taskID = tasks[0].ID
		return true
	}, 2*time.Second, 10*time.Millisecond)

	pr, pw := io.Pipe()
	upstreamReady := make(chan struct{})
	upstream := &routesNonRetainingUpstream{
		fn: func(req *http.Request) (*http.Response, error) {
			if req != nil && req.Body != nil {
				_, _ = io.Copy(io.Discard, req.Body)
				_ = req.Body.Close()
			}
			close(upstreamReady)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       pr,
				Request:    req,
			}, nil
		},
	}

	groupID := int64(4251)
	account := service.Account{
		ID:          9951,
		Name:        "e2e-midstream-openai",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		GroupIDs:    []int64{groupID},
		Credentials: map[string]any{"api_key": "fixture-key", "base_url": "http://127.0.0.1:1"},
		Extra:       map[string]any{"openai_passthrough": true},
	}

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.MaxAccountSwitches = 2

	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gatewaySvc := service.NewOpenAIGatewayService(
		&routesAccountRepoStub{account: account}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache,
		upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	openAIHandler := handler.NewOpenAIGatewayHandler(
		gatewaySvc,
		service.NewConcurrencyService(nil),
		billingCache,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg,
	)

	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []service.CompositeModelRoute{
			{
				ID:             1,
				GroupID:        groupID,
				PublicModel:    "openrouter/gpt-5",
				MatchType:      service.CompositeRouteMatchExact,
				TargetPlatform: service.PlatformOpenAI,
				UpstreamModel:  "gpt-5",
				Endpoint:       service.CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        true,
			},
		},
	})

	key := &service.APIKey{
		ID:      1851,
		UserID:  1751,
		GroupID: &groupID,
		User:    &service.User{ID: 1751, Status: service.StatusActive},
		Group: &service.Group{
			ID:             groupID,
			Platform:       service.PlatformComposite,
			Status:         service.StatusActive,
			Hydrated:       true,
			ModelAllowlist: service.GroupModelAllowlist{Enabled: true, Models: []string{"openrouter/gpt-5", "gpt-5"}},
		},
	}

	var weakOrig, weakRewritten weak.Pointer[byte]

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(servermiddleware.ContextKeyAPIKey), key)
		c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: key.User.ID})
		c.Next()
	})
	router.Use(servermiddleware.RequestCapture(captureMgr))
	router.Use(servermiddleware.GroupModelAllowlist())
	router.Use(servermiddleware.RejectConflictingRequestFields())
	router.Use(func(c *gin.Context) {
		if pb, ok := c.Request.Body.(*pkghttputil.PrereadBody); ok && len(pb.Bytes()) > 0 {
			weakOrig = weak.Make(&pb.Bytes()[0])
		}
		c.Next()
	})
	router.Use(compositeTargetPlatformMiddleware(resolver))
	router.Use(servermiddleware.GatewayIPNotice(nil, nil))
	router.Use(func(c *gin.Context) {
		if pb, ok := c.Request.Body.(*pkghttputil.PrereadBody); ok && len(pb.Bytes()) > 0 {
			weakRewritten = weak.Make(&pb.Bytes()[0])
		}
		c.Next()
	})
	router.POST("/openai/v1/responses", openAIHandler.Responses)

	payload := `{"model":"openrouter/gpt-5","stream":true,"input":"` + strings.Repeat("midstream-proof-", 16<<10) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	sseWriter := newRoutesSSEResponseWriter()

	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(sseWriter, req)
	}()

	select {
	case <-upstreamReady:
	case <-done:
		t.Fatalf("handler exited before calling upstream: status=%d body=%s", sseWriter.StatusCode(), sseWriter.Snapshot())
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for upstreamReady: status=%d body=%s", sseWriter.StatusCode(), sseWriter.Snapshot())
	}
	// Stage 1: response.created must NOT drop the request body yet while pre-output failover is active.
	_, _ = io.WriteString(pw, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_e2e_1\",\"model\":\"gpt-5\"}}\n\n")
	time.Sleep(30 * time.Millisecond)
	runtime.GC()
	require.NotNil(t, weakRewritten.Value(), "rewritten body must stay alive after response.created while pre-output failover is still possible")

	// Stage 2: first output delta triggers mid-stream release WHILE io.Pipe stays open!
	_, _ = io.WriteString(pw, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
	waitRoutesOutput(t, sseWriter, "response.output_text.delta")

	require.Eventually(t, func() bool {
		runtime.GC()
		return weakOrig.Value() == nil
	}, 3*time.Second, 5*time.Millisecond, "original request buffer must be collected mid-stream")
	require.Eventually(t, func() bool {
		runtime.GC()
		return weakRewritten.Value() == nil
	}, 3*time.Second, 5*time.Millisecond, "rewritten request buffer must be collected mid-stream while io.Pipe is still open")

	// Stage 3: finish stream and verify RequestCapture persisted full request bytes.
	_, _ = io.WriteString(pw, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_e2e_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":4}}}\n\n")
	_ = pw.Close()
	<-done

	require.Equal(t, http.StatusOK, sseWriter.StatusCode())
	require.Contains(t, sseWriter.Snapshot(), "response.completed")

	require.Eventually(t, func() bool {
		recs, rErr := captureStore.Records(t.Context(), "", "", false, 10, 0)
		return rErr == nil && len(recs) == 1 && recs[0].FinishedAt != nil
	}, 3*time.Second, 10*time.Millisecond)

	var archive bytes.Buffer
	captureMgr.ApplyConfig(requestcapture.Config{
		Enabled:              false,
		QuotaMiB:             64,
		RetentionDays:        1,
		ID1DiagnosticEnabled: false,
	})
	require.Eventually(t, func() bool {
		archive.Reset()
		return captureMgr.Export(t.Context(), &archive, taskID, "") == nil
	}, 3*time.Second, 10*time.Millisecond)
	gz, err := gzip.NewReader(&archive)
	require.NoError(t, err)
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	var capturedClientReq string
	for {
		hdr, tErr := tr.Next()
		if tErr == io.EOF {
			break
		}
		require.NoError(t, tErr)
		if strings.Contains(hdr.Name, "client_request") {
			b, rErr := io.ReadAll(tr)
			require.NoError(t, rErr)
			capturedClientReq = string(b)
		}
	}
	require.Contains(t, capturedClientReq, "midstream-proof-", "RequestCapture must retain full captured client_request even after mid-stream GC")
}
