package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// modelCatalogFileURL is the curated model catalogue published with the
	// repository. Editing catalog/models.json and pushing to main updates
	// metadata and reference pricing without shipping a new binary.
	defaultModelCatalogFileURL = "https://raw.githubusercontent.com/kiss-kedaya/sub2api/main/catalog/models.json"
	modelCatalogFileTTL        = 6 * time.Hour
	modelCatalogFileRetryDelay = 5 * time.Minute
	modelCatalogFileTimeout    = 20 * time.Second
	modelCatalogFileMaxBytes   = 4 << 20
)

// modelCatalogFilePayload is the schema of catalog/models.json.
type modelCatalogFilePayload struct {
	Schema    int                              `json:"schema"`
	UpdatedAt string                           `json:"updated_at"`
	Models    map[string]UpstreamModelMetadata `json:"models"`
}

// ModelCatalogFileSource keeps the last good copy of the curated catalogue and
// refreshes it in the background. Lookups never block on the network and never
// fail because the remote file is unavailable: the previous snapshot (or the
// embedded defaults) keeps serving.
type ModelCatalogFileSource struct {
	url    string
	client *http.Client
	ttl    time.Duration
	now    func() time.Time

	mu          sync.RWMutex
	models      map[string]UpstreamModelMetadata
	normalized  map[string]string
	etag        string
	fetchedAt   time.Time
	lastAttempt time.Time
	lastErr     string

	fetchMu  sync.Mutex
	fetching bool
}

// NewModelCatalogFileSource builds a source for the given URL. An empty URL
// disables the remote catalogue and keeps the embedded built-ins in charge.
func NewModelCatalogFileSource(url string, client *http.Client) *ModelCatalogFileSource {
	url = strings.TrimSpace(url)
	if client == nil {
		client = &http.Client{Timeout: modelCatalogFileTimeout}
	}
	return &ModelCatalogFileSource{
		url:    url,
		client: client,
		ttl:    modelCatalogFileTTL,
		now:    time.Now,
	}
}

func (s *ModelCatalogFileSource) enabled() bool {
	return s != nil && s.url != ""
}

func (s *ModelCatalogFileSource) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

// Seed installs a snapshot without a network call (tests and warm starts).
func (s *ModelCatalogFileSource) Seed(payload modelCatalogFilePayload, fetchedAt time.Time) error {
	if s == nil {
		return fmt.Errorf("model catalogue source is nil")
	}
	if err := validateModelCatalogFilePayload(payload); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models = payload.Models
	s.normalized = buildModelCatalogFileIndex(payload.Models)
	s.fetchedAt = fetchedAt
	return nil
}

// Lookup resolves a model id against the last good snapshot. Callers get the
// entry plus true only for a real hit; a miss is not an error.
func (s *ModelCatalogFileSource) Lookup(modelID string) (UpstreamModelMetadata, bool) {
	if s == nil {
		return UpstreamModelMetadata{}, false
	}
	// A disabled source means "do not fetch"; a seeded snapshot (tests, warm
	// starts) is still usable.
	base := strings.ToLower(strings.TrimSpace(modelID))
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		base = strings.TrimSpace(base[slash+1:])
	}
	if base == "" {
		return UpstreamModelMetadata{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if entry, ok := s.models[base]; ok {
		return entry, true
	}
	if id, ok := s.normalized[base]; ok {
		if entry, found := s.models[id]; found {
			return entry, true
		}
	}
	for _, candidate := range normalizedModelIdentifiers(base) {
		if entry, ok := s.models[candidate]; ok {
			return entry, true
		}
		if id, ok := s.normalized[candidate]; ok {
			if entry, found := s.models[id]; found {
				return entry, true
			}
		}
	}
	return UpstreamModelMetadata{}, false
}

// Refresh fetches the catalogue once. A 304 keeps the current snapshot and
// only refreshes the TTL; any other failure keeps the last good data.
func (s *ModelCatalogFileSource) Refresh(ctx context.Context) error {
	if !s.enabled() {
		return nil
	}
	s.fetchMu.Lock()
	if s.fetching {
		s.fetchMu.Unlock()
		return nil
	}
	s.fetching = true
	s.fetchMu.Unlock()
	defer func() {
		s.fetchMu.Lock()
		s.fetching = false
		s.fetchMu.Unlock()
	}()

	now := s.clock()
	s.mu.Lock()
	s.lastAttempt = now
	etag := s.etag
	s.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, modelCatalogFileTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		s.recordError(err)
		return err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		s.recordError(err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		s.mu.Lock()
		s.fetchedAt = now
		s.lastErr = ""
		s.mu.Unlock()
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("model catalogue returned status %d", resp.StatusCode)
		s.recordError(err)
		return err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modelCatalogFileMaxBytes+1))
	if err != nil {
		s.recordError(err)
		return err
	}
	if len(body) > modelCatalogFileMaxBytes {
		err := fmt.Errorf("model catalogue exceeds %d bytes", modelCatalogFileMaxBytes)
		s.recordError(err)
		return err
	}
	var payload modelCatalogFilePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.recordError(err)
		return err
	}
	if err := validateModelCatalogFilePayload(payload); err != nil {
		s.recordError(err)
		return err
	}
	s.mu.Lock()
	s.models = payload.Models
	s.normalized = buildModelCatalogFileIndex(payload.Models)
	s.etag = strings.TrimSpace(resp.Header.Get("ETag"))
	s.fetchedAt = now
	s.lastErr = ""
	s.mu.Unlock()
	slog.Info("model catalogue refreshed", "models", len(payload.Models), "updated_at", payload.UpdatedAt)
	return nil
}

func (s *ModelCatalogFileSource) recordError(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
	slog.Warn("model catalogue refresh failed; keeping the last snapshot", "error", err)
}

// ensureAsync triggers a refresh when the snapshot is missing or older than the
// TTL. The first call is served from embedded defaults while the fetch runs.
func (s *ModelCatalogFileSource) ensureAsync(ctx context.Context) {
	if !s.enabled() {
		return
	}
	now := s.clock()
	s.mu.RLock()
	fresh := len(s.models) > 0 && now.Sub(s.fetchedAt) < s.ttl
	retrying := !s.lastAttempt.IsZero() && now.Sub(s.lastAttempt) < modelCatalogFileRetryDelay
	s.mu.RUnlock()
	if fresh || retrying {
		return
	}
	fetchCtx := context.Background()
	if ctx != nil {
		fetchCtx = context.WithoutCancel(ctx)
	}
	go func() {
		_ = s.Refresh(fetchCtx)
	}()
}

// Status reports the current snapshot for admin/diagnostics.
func (s *ModelCatalogFileSource) Status() (url string, models int, fetchedAt time.Time, lastErr string, enabled bool) {
	if s == nil {
		return "", 0, time.Time{}, "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.url, len(s.models), s.fetchedAt, s.lastErr, s.enabled()
}

func validateModelCatalogFilePayload(payload modelCatalogFilePayload) error {
	if payload.Schema < 1 {
		return fmt.Errorf("model catalogue schema %d is not supported", payload.Schema)
	}
	if len(payload.Models) == 0 {
		return fmt.Errorf("model catalogue has no models")
	}
	for id, entry := range payload.Models {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("model catalogue contains an empty model id")
		}
		if entry.ContextWindow < 0 || entry.MaxOutputTokens < 0 {
			return fmt.Errorf("model catalogue entry %q has a negative limit", id)
		}
	}
	return nil
}

func buildModelCatalogFileIndex(models map[string]UpstreamModelMetadata) map[string]string {
	index := make(map[string]string, len(models))
	for id := range models {
		lower := strings.ToLower(strings.TrimSpace(id))
		if _, exists := index[lower]; !exists {
			index[lower] = id
		}
		for _, candidate := range normalizedModelIdentifiers(id) {
			if _, exists := index[candidate]; !exists {
				index[candidate] = id
			}
		}
	}
	return index
}

// modelCatalogFileURLFromEnv returns the configured URL, defaulting to the
// repository copy. "off" disables the remote catalogue entirely.
func modelCatalogFileURLFromEnv() string {
	value := strings.TrimSpace(os.Getenv("SUB2API_MODEL_CATALOG_URL"))
	if value == "" {
		return defaultModelCatalogFileURL
	}
	if strings.EqualFold(value, "off") || strings.EqualFold(value, "disabled") || value == "-" {
		return ""
	}
	return value
}
