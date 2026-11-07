package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func catalogFileEntry(ctx int64, out int64, input []string) UpstreamModelMetadata {
	reasoning, toolCall := true, true
	return UpstreamModelMetadata{
		DisplayName:      "DeepSeek V4.1 Flash",
		Description:      "official",
		ContextWindow:    ctx,
		MaxOutputTokens:  out,
		InputModalities:  input,
		OutputModalities: []string{"text"},
		Reasoning:        &reasoning,
		ToolCall:         &toolCall,
		Family:           "deepseek-v4.1",
	}
}

func TestModelCatalogFileRefreshLookupAndETag(t *testing.T) {
	var notModified atomic.Int32
	payload := modelCatalogFilePayload{Schema: 1, UpdatedAt: "2026-11-07", Models: map[string]UpstreamModelMetadata{
		"deepseek-flash": catalogFileEntry(1048576, 393216, []string{"text", "image"}),
	}}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	source := NewModelCatalogFileSource(srv.URL, srv.Client())
	require.NoError(t, source.Refresh(context.Background()))
	entry, ok := source.Lookup("deepseek-flash")
	require.True(t, ok)
	require.Equal(t, int64(1048576), entry.ContextWindow)
	require.Equal(t, int64(393216), entry.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, entry.InputModalities)

	// 304 keeps the snapshot and only refreshes the TTL.
	require.NoError(t, source.Refresh(context.Background()))
	require.Equal(t, int32(1), notModified.Load())
	entry, ok = source.Lookup("deepseek-flash")
	require.True(t, ok)
	require.Equal(t, int64(393216), entry.MaxOutputTokens)
}

func TestModelCatalogFileInvalidPayloadKeepsLastGood(t *testing.T) {
	var broken atomic.Bool
	good, err := json.Marshal(modelCatalogFilePayload{Schema: 1, Models: map[string]UpstreamModelMetadata{
		"kimi-k2.8": catalogFileEntry(262144, 65536, []string{"text", "image"}),
	}})
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() {
			_, _ = w.Write([]byte(`{"schema":1,"models":{}}`))
			return
		}
		_, _ = w.Write(good)
	}))
	defer srv.Close()

	source := NewModelCatalogFileSource(srv.URL, srv.Client())
	require.NoError(t, source.Refresh(context.Background()))
	broken.Store(true)
	require.Error(t, source.Refresh(context.Background()))
	entry, ok := source.Lookup("kimi-k2.8")
	require.True(t, ok, "a rejected refresh must keep the previous snapshot")
	require.Equal(t, int64(65536), entry.MaxOutputTokens)
}

func TestModelCatalogFileLookupNormalizesDottedAndDatedIDs(t *testing.T) {
	source := NewModelCatalogFileSource("https://example.invalid/models.json", nil)
	fetched := time.Now()
	require.NoError(t, source.Seed(modelCatalogFilePayload{Schema: 1, Models: map[string]UpstreamModelMetadata{
		"claude-opus-4-5":          catalogFileEntry(1000000, 128000, []string{"text", "image"}),
		"deepseek-v4.1-flash-1017": catalogFileEntry(1048576, 393216, []string{"text", "image"}),
	}}, fetched))
	for _, id := range []string{"claude-opus-4.5", "anthropic/claude-opus-4.5", "claude-opus-4-5-20251101"} {
		_, ok := source.Lookup(id)
		require.True(t, ok, id)
	}
	for _, id := range []string{"deepseek-v4.1-flash-1017", "deepseek-v4.1-flash-0910", "deepseek-v4.1-flash"} {
		_, ok := source.Lookup(id)
		require.True(t, ok, id)
	}
}

func TestModelCatalogFileOverridesModelsDevResellerValues(t *testing.T) {
	fetched := time.Now()
	catalog := NewModelMetadataCatalog(nil)
	registry := map[string]modelsDevProvider{
		"reseller": {Models: map[string]modelsDevModel{
			"deepseek-v4.1-flash-1017": {
				ID: "deepseek-v4.1-flash-1017", Name: "DeepSeek V4.1 Flash",
				Limit:      modelsDevLimit{Context: 1050000, Output: 1050000},
				Modalities: modelsDevModalities{Input: []string{"text"}, Output: []string{"text"}},
			},
		}},
	}
	catalog.SeedRegistry(registry, fetched)
	reasoning, toolCall := true, true
	require.NoError(t, catalog.fileCatalog.Seed(modelCatalogFilePayload{Schema: 1, Models: map[string]UpstreamModelMetadata{
		"deepseek-v4.1-flash-1017": {
			DisplayName: "DeepSeek V4.1 Flash 1017", ContextWindow: 1048576, MaxOutputTokens: 393216,
			InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"},
			Reasoning: &reasoning, ToolCall: &toolCall, Family: "deepseek-v4.1",
			ReferencePricing: &ModelCatalogPricing{Unit: "USD/1M tokens"},
		},
	}}, fetched))

	metadata := catalog.Resolve(context.Background(), "deepseek-v4.1-flash-1017", "openai", nil)
	require.Equal(t, ModelMetadataSourceCatalog, metadata.Source)
	require.Equal(t, int64(1048576), metadata.ContextWindow)
	require.Equal(t, int64(393216), metadata.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, metadata.InputModalities)
	require.NotNil(t, metadata.Capabilities)
	require.True(t, metadata.Capabilities.Reasoning)
	require.NotNil(t, metadata.ReferencePricing)
}

func TestModelCatalogFileDisabledKeepsRegistry(t *testing.T) {
	fetched := time.Now()
	catalog := NewModelMetadataCatalog(nil)
	catalog.fileCatalog = NewModelCatalogFileSource("", nil)
	catalog.SeedRegistry(map[string]modelsDevProvider{
		"openai": {Models: map[string]modelsDevModel{
			"gpt-foo": {ID: "gpt-foo", Name: "GPT Foo", Limit: modelsDevLimit{Context: 1050000, Output: 128000}},
		}},
	}, fetched)
	metadata := catalog.Resolve(context.Background(), "gpt-foo", "openai", nil)
	require.Equal(t, int64(1050000), metadata.ContextWindow)
	require.Equal(t, ModelMetadataSourceModelsDev, metadata.Source)
}

// The repository catalogue is edited by hand; a malformed commit must fail CI
// instead of shipping a broken snapshot that every instance would reject.
func TestRepositoryCatalogModelsFileIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../../catalog/models.json")
	require.NoError(t, err)
	var payload modelCatalogFilePayload
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.NoError(t, validateModelCatalogFilePayload(payload))
	flash, ok := payload.Models["deepseek-flash"]
	require.True(t, ok)
	require.Equal(t, int64(1048576), flash.ContextWindow)
	require.Equal(t, int64(393216), flash.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, flash.InputModalities)
	pro, ok := payload.Models["deepseek-v4-pro"]
	require.True(t, ok)
	require.Equal(t, []string{"text"}, pro.InputModalities, "v4-pro has no vision")
}
