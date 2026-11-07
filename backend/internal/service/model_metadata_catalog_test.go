package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testBoolPointer(value bool) *bool        { return &value }
func testFloatPointer(value float64) *float64 { return &value }

// testPricingResolver builds the same exact-then-wildcard lookup billing uses.
func testPricingResolver(pricing []ChannelModelPricing, platform string) ModelPricingResolverFunc {
	return func(modelID string) *ChannelModelPricing {
		return findPricingForModel(pricing, platform, strings.ToLower(strings.TrimSpace(modelID)))
	}
}

// testModelsDevRegistry mirrors the shape of https://models.dev/api.json for
// the IDs this gateway serves, including a reseller entry so provider ranking
// is exercised.
func testModelsDevRegistry() map[string]modelsDevProvider {
	return map[string]modelsDevProvider{
		"deepseek": {
			ID:   "deepseek",
			Name: "DeepSeek",
			API:  "https://api.deepseek.com",
			Models: map[string]modelsDevModel{
				"deepseek-v4.1-flash": {
					ID:               "deepseek-v4.1-flash",
					Name:             "DeepSeek V4.1 Flash",
					Description:      "DeepSeek V4.1 Flash with 1M context.",
					Reasoning:        testBoolPointer(true),
					ToolCall:         testBoolPointer(true),
					StructuredOutput: testBoolPointer(true),
					Attachment:       testBoolPointer(true),
					Modalities:       modelsDevModalities{Input: []string{"text", "image"}, Output: []string{"text"}},
					Limit:            modelsDevLimit{Context: 1_048_576, Output: 384_000},
					Cost: modelsDevCost{
						Input:      testFloatPointer(0.165),
						Output:     testFloatPointer(0.66),
						CacheRead:  testFloatPointer(0.0033),
						CacheWrite: testFloatPointer(0.02),
					},
					Family:      "deepseek-flash",
					ReleaseDate: "2026-10-17",
					Knowledge:   "2025-05",
					OpenWeights: testBoolPointer(true),
				},
			},
		},
		// Reseller entry: same ID, different price. The first-party provider wins.
		"reseller": {
			ID:   "reseller",
			Name: "Reseller",
			API:  "https://reseller.example/v1",
			Models: map[string]modelsDevModel{
				"deepseek-v4.1-flash": {
					ID:          "deepseek-v4.1-flash",
					Name:        "Reseller DeepSeek",
					Description: "Reseller copy.",
					Limit:       modelsDevLimit{Context: 1_000_000, Output: 384_000},
					Cost:        modelsDevCost{Input: testFloatPointer(9.99)},
				},
			},
		},
		"zhipuai": {
			ID:   "zhipuai",
			Name: "Zhipu AI",
			Models: map[string]modelsDevModel{
				"glm-5.3": {
					ID:               "glm-5.3",
					Name:             "GLM-5.3",
					Description:      "GLM-5.3 general purpose model.",
					Reasoning:        testBoolPointer(true),
					ToolCall:         testBoolPointer(true),
					StructuredOutput: testBoolPointer(false),
					Attachment:       testBoolPointer(false),
					Modalities:       modelsDevModalities{Input: []string{"text", "image", "pdf"}, Output: []string{"text"}},
					Limit:            modelsDevLimit{Context: 1_000_000, Output: 131_072},
					Family:           "glm",
					ReleaseDate:      "2026-08-14",
				},
			},
		},
		"openai": {
			ID:   "openai",
			Name: "OpenAI",
			Models: map[string]modelsDevModel{
				"gpt-image-2": {
					ID:          "gpt-image-2",
					Name:        "GPT Image 2",
					Description: "Image model for prompt-driven generation and editing.",
					Reasoning:   testBoolPointer(false),
					ToolCall:    testBoolPointer(false),
					Attachment:  testBoolPointer(true),
					Modalities:  modelsDevModalities{Input: []string{"text", "image"}, Output: []string{"image"}},
					Limit:       modelsDevLimit{Context: 0, Output: 0},
					Family:      "gpt-image",
					ReleaseDate: "2026-04-21",
					Cost:        modelsDevCost{Input: testFloatPointer(5), Output: testFloatPointer(30)},
				},
			},
		},
	}
}

// newTestModelMetadataCatalog seeds a registry and marks the snapshot fresh so
// tests never start a registry fetch.
// newTestModelMetadataCatalogWithClient keeps unit tests off the network: the
// curated catalogue is fetched from GitHub by default, and a reachable network
// must not change assertions about models.dev sources.
func newTestModelMetadataCatalogWithClient(client *http.Client) *ModelMetadataCatalog {
	catalog := NewModelMetadataCatalog(client)
	catalog.fileCatalog = NewModelCatalogFileSource("", nil)
	return catalog
}

func newTestModelMetadataCatalog(registry map[string]modelsDevProvider) *ModelMetadataCatalog {
	catalog := newTestModelMetadataCatalogWithClient(nil)
	catalog.SeedRegistry(registry, time.Now())
	return catalog
}

func TestUpstreamMetadataFromModelsDevModelCarriesCatalogueFields(t *testing.T) {
	model := modelsDevModel{
		ID:               "deepseek-v4.1-flash",
		Name:             "DeepSeek V4.1 Flash",
		Reasoning:        testBoolPointer(true),
		ToolCall:         testBoolPointer(true),
		StructuredOutput: testBoolPointer(false),
		Attachment:       testBoolPointer(true),
		Temperature:      testBoolPointer(false),
		OpenWeights:      testBoolPointer(true),
		Modalities:       modelsDevModalities{Input: []string{"text", "image"}, Output: []string{"text"}},
		Limit:            modelsDevLimit{Context: 1_048_576, Output: 384_000},
		Cost:             modelsDevCost{Input: testFloatPointer(0.165), Output: testFloatPointer(0.66)},
		Family:           "deepseek-flash",
		ReleaseDate:      "2026-10-17",
		Knowledge:        "2025-05",
		CanonicalModelID: "deepseek/deepseek-v4.1-flash-1017",
	}

	metadata := upstreamMetadataFromModelsDevModel("deepseek-v4.1-flash-1017", model)

	require.Equal(t, "deepseek-v4.1-flash", metadata.ID, "the registry ID wins, matching existing snapshot semantics")
	require.Equal(t, []string{"text"}, metadata.OutputModalities)
	require.NotNil(t, metadata.ToolCall)
	require.True(t, *metadata.ToolCall)
	require.NotNil(t, metadata.StructuredOutput)
	require.False(t, *metadata.StructuredOutput)
	require.NotNil(t, metadata.Attachment)
	require.True(t, *metadata.Attachment)
	require.NotNil(t, metadata.Temperature)
	require.False(t, *metadata.Temperature)
	require.NotNil(t, metadata.OpenWeights)
	require.True(t, *metadata.OpenWeights)
	require.Equal(t, "deepseek-flash", metadata.Family)
	require.Equal(t, "2026-10-17", metadata.ReleaseDate)
	require.Equal(t, "2025-05", metadata.Knowledge)
	require.NotNil(t, metadata.ReferencePricing)
	require.InDelta(t, 0.165, *metadata.ReferencePricing.Input, 1e-9)

	merged, changed := mergeUpstreamModelMetadata(UpstreamModelMetadata{ID: metadata.ID}, metadata)
	require.True(t, changed)
	require.Equal(t, metadata.Family, merged.Family)
	require.Equal(t, metadata.ReleaseDate, merged.ReleaseDate)
	require.NotNil(t, merged.ReferencePricing)
}

func TestModelMetadataCatalogResolveExactMatch(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	metadata := catalog.Resolve(context.Background(), "deepseek-v4.1-flash", PlatformOpenAI, nil)

	require.Equal(t, ModelMetadataSourceModelsDev, metadata.Source)
	require.Equal(t, "deepseek-v4.1-flash", metadata.ID)
	require.Equal(t, "DeepSeek V4.1 Flash", metadata.DisplayName)
	require.Equal(t, "DeepSeek V4.1 Flash with 1M context.", metadata.Description)
	require.Equal(t, int64(1_048_576), metadata.ContextWindow)
	require.Equal(t, int64(393_216), metadata.MaxOutputTokens, "the built-in 384K (=393216) beats the registry's rounded 384000")
	require.Equal(t, []string{"text", "image"}, metadata.InputModalities)
	require.Equal(t, []string{"text"}, metadata.OutputModalities)
	require.Equal(t, "deepseek-v4.1", metadata.Family, "authoritative first-party family wins")
	require.Equal(t, "2026-10-17", metadata.ReleaseDate)
	require.Equal(t, "2025-05", metadata.Knowledge)
	require.NotNil(t, metadata.OpenWeights)
	require.True(t, *metadata.OpenWeights)
	require.NotNil(t, metadata.Capabilities)
	require.True(t, metadata.Capabilities.Reasoning)
	require.True(t, metadata.Capabilities.ToolCall)
	require.True(t, metadata.Capabilities.StructuredOutput)
	require.True(t, metadata.Capabilities.Attachment)
	require.NotNil(t, metadata.ReferencePricing)
	require.Equal(t, "USD/1M tokens", metadata.ReferencePricing.Unit)
	require.NotNil(t, metadata.ReferencePricing.Input)
	require.InDelta(t, 0.165, *metadata.ReferencePricing.Input, 1e-9)
	// A first-party provider wins over a reseller listing the same ID.
	require.NotEqual(t, 9.99, *metadata.ReferencePricing.Input)
	require.Nil(t, metadata.Pricing, "no configured price card was passed")
}

func TestModelMetadataCatalogDerivesDisplayNameWhenRegistryEchoesID(t *testing.T) {
	registry := map[string]modelsDevProvider{
		"openai": {ID: "openai", Models: map[string]modelsDevModel{
			"gpt-image-2": {ID: "gpt-image-2", Name: "gpt-image-2", Family: "gpt-image"},
		}},
	}
	catalog := newTestModelMetadataCatalog(registry)
	metadata := catalog.Resolve(context.Background(), "gpt-image-2", PlatformOpenAI, nil)
	require.Equal(t, ModelMetadataSourceModelsDev, metadata.Source)
	require.Equal(t, "GPT Image 2", metadata.DisplayName, "raw IDs are not display names")
}

func TestModelMetadataCatalogResolveNormalizedMatches(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	displayNames := map[string]string{
		"deepseek-v4.1-flash-expires-on-0910": "DeepSeek V4.1 Flash",
		"deepseek-v4.1-flash-0910":            "DeepSeek V4.1 Flash 0910",
		"deepseek-v4.1-flash-high":            "DeepSeek V4.1 Flash",
		"DeepSeek-V4.1-Flash":                 "DeepSeek V4.1 Flash",
		"deepseek/deepseek-v4.1-flash":        "DeepSeek V4.1 Flash",
	}
	for modelID, wantDisplay := range displayNames {
		t.Run(modelID, func(t *testing.T) {
			metadata := catalog.Resolve(context.Background(), modelID, PlatformOpenAI, nil)
			require.Equal(t, ModelMetadataSourceModelsDev, metadata.Source, modelID)
			require.Equal(t, wantDisplay, metadata.DisplayName, modelID)
			require.Equal(t, int64(1_048_576), metadata.ContextWindow, modelID)
			require.Equal(t, int64(393_216), metadata.MaxOutputTokens, modelID)
			require.NotNil(t, metadata.ReferencePricing, modelID)
		})
	}
}

func TestModelMetadataCatalogResolveFamilyFallback(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	metadata := catalog.Resolve(context.Background(), "glm-5.3-prime", PlatformOpenAI, nil)

	require.Equal(t, ModelMetadataSourceFamily, metadata.Source)
	require.Equal(t, "GLM-5.3", metadata.DisplayName)
	require.Equal(t, int64(1_000_000), metadata.ContextWindow)
	require.Equal(t, []string{"text", "image", "pdf"}, metadata.InputModalities)
	require.Equal(t, "glm", metadata.Family)
	require.Nil(t, metadata.ReferencePricing, "a sibling's price must never be borrowed")
	require.Nil(t, metadata.Pricing)
}

func TestModelMetadataCatalogResolveFamilyShapeFallback(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	metadata := catalog.Resolve(context.Background(), "gpt-image-2.6-sunburst", PlatformOpenAI, nil)

	require.Equal(t, ModelMetadataSourceFamily, metadata.Source)
	// Built-in first-party metadata supplies the version-accurate display name.
	require.Equal(t, "GPT Image 2.6 Sunburst", metadata.DisplayName)
	require.Equal(t, []string{"text", "image"}, metadata.InputModalities)
	require.Equal(t, []string{"image"}, metadata.OutputModalities)
	require.Equal(t, "gpt-image", metadata.Family)
	require.Nil(t, metadata.ReferencePricing)
	require.Nil(t, metadata.Pricing)
}

func TestModelMetadataCatalogResolveUnknownModelKeepsStandardShape(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	metadata := catalog.Resolve(context.Background(), "acme-unknown-9", PlatformOpenAI, nil)

	require.Equal(t, ModelMetadataSourceNone, metadata.Source)
	require.Equal(t, "Acme Unknown 9", metadata.DisplayName)
	require.Empty(t, metadata.Description)
	require.Zero(t, metadata.ContextWindow)
	require.Nil(t, metadata.ReferencePricing)
	require.Nil(t, metadata.Pricing)
}

func TestModelMetadataCatalogConfiguredPriceWinsOverReference(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	pricing := []ChannelModelPricing{{
		Platform:       PlatformOpenAI,
		Models:         []string{"deepseek-v4.1-flash"},
		BillingMode:    BillingModeToken,
		InputPrice:     testFloatPointer(0.00000015),
		OutputPrice:    testFloatPointer(0.0000006),
		CacheReadPrice: testFloatPointer(0.000000003),
	}}
	metadata := catalog.Resolve(context.Background(), "deepseek-v4.1-flash", PlatformOpenAI, testPricingResolver(pricing, PlatformOpenAI))

	require.NotNil(t, metadata.Pricing)
	require.Equal(t, "USD/1M tokens", metadata.Pricing.Unit)
	require.InDelta(t, 0.15, *metadata.Pricing.Input, 1e-9)
	require.InDelta(t, 0.6, *metadata.Pricing.Output, 1e-9)
	require.InDelta(t, 0.003, *metadata.Pricing.CacheRead, 1e-9)
	require.Nil(t, metadata.Pricing.CacheWrite, "cache-write is unset on the price card")

	require.NotNil(t, metadata.ReferencePricing)
	require.InDelta(t, 0.165, *metadata.ReferencePricing.Input, 1e-9,
		"models.dev price stays in reference_pricing")
	require.Nil(t, metadata.RequestPricing)
}

func TestModelMetadataCatalogWildcardPriceCardAndPerRequest(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	pricing := []ChannelModelPricing{{
		Platform:        PlatformOpenAI,
		Models:          []string{"gpt-image-*"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: testFloatPointer(0.04),
	}}
	metadata := catalog.Resolve(context.Background(), "gpt-image-2", PlatformOpenAI, testPricingResolver(pricing, PlatformOpenAI))

	require.Nil(t, metadata.Pricing, "no token price configured")
	require.NotNil(t, metadata.RequestPricing)
	require.InDelta(t, 0.04, *metadata.RequestPricing.PerRequest, 1e-9)
	require.Equal(t, "USD/request", metadata.RequestPricing.Unit)
}

func TestModelMetadataCatalogEnrichModelsListBody(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	pricing := []ChannelModelPricing{{
		Platform:    PlatformOpenAI,
		Models:      []string{"deepseek-v4.1-flash"},
		BillingMode: BillingModeToken,
		InputPrice:  testFloatPointer(0.00000015),
		OutputPrice: testFloatPointer(0.0000006),
	}}
	body := []byte(`{"object":"list","data":[
		{"id":"deepseek-v4.1-flash","object":"model","created":1704067200,"owned_by":"openai","type":"model","display_name":"deepseek-v4.1-flash"},
		{"id":"acme-unknown-9","object":"model","created":1704067200,"owned_by":"openai","type":"model","display_name":"acme-unknown-9"}
	]}`)

	enriched, err := catalog.EnrichModelsListBody(context.Background(), body, PlatformOpenAI, testPricingResolver(pricing, PlatformOpenAI))
	require.NoError(t, err)

	var catalogBody struct {
		Object string `json:"object"`
		Data   []struct {
			ID               string               `json:"id"`
			Object           string               `json:"object"`
			Created          int64                `json:"created"`
			OwnedBy          string               `json:"owned_by"`
			Type             string               `json:"type"`
			DisplayName      string               `json:"display_name"`
			Description      string               `json:"description"`
			ContextWindow    int64                `json:"context_window"`
			MaxOutputTokens  int64                `json:"max_output_tokens"`
			InputModalities  []string             `json:"input_modalities"`
			OutputModalities []string             `json:"output_modalities"`
			Capabilities     *ModelCapabilities   `json:"capabilities"`
			Pricing          *ModelCatalogPricing `json:"pricing"`
			ReferencePricing *ModelCatalogPricing `json:"reference_pricing"`
			Family           string               `json:"family"`
			ReleaseDate      string               `json:"release_date"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enriched, &catalogBody))
	require.Equal(t, "list", catalogBody.Object)
	require.Len(t, catalogBody.Data, 2)

	known := catalogBody.Data[0]
	require.Equal(t, "deepseek-v4.1-flash", known.ID)
	require.Equal(t, "model", known.Object)
	require.EqualValues(t, 1704067200, known.Created)
	require.Equal(t, "openai", known.OwnedBy)
	require.Equal(t, "model", known.Type, "standard fields must be preserved")
	require.Equal(t, "DeepSeek V4.1 Flash", known.DisplayName)
	require.NotEmpty(t, known.Description)
	require.EqualValues(t, 1_048_576, known.ContextWindow)
	require.EqualValues(t, 393_216, known.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, known.InputModalities)
	require.Equal(t, []string{"text"}, known.OutputModalities)
	require.NotNil(t, known.Capabilities)
	require.True(t, known.Capabilities.ToolCall)
	require.NotNil(t, known.Pricing)
	require.InDelta(t, 0.15, *known.Pricing.Input, 1e-9)
	require.NotNil(t, known.ReferencePricing)
	require.Equal(t, "deepseek-v4.1", known.Family)
	require.Equal(t, "2026-10-17", known.ReleaseDate)

	unknown := catalogBody.Data[1]
	require.Equal(t, "acme-unknown-9", unknown.ID)
	require.Equal(t, "model", unknown.Object)
	require.EqualValues(t, 1704067200, unknown.Created)
	require.Equal(t, "openai", unknown.OwnedBy)
	require.Equal(t, "Acme Unknown 9", unknown.DisplayName)
	require.Nil(t, unknown.Pricing)
}

func TestModelMetadataCatalogEnrichKeepsExistingUpstreamFields(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	body := []byte(`{"data":[{"id":"deepseek-v4.1-flash","object":"model","created":1,"owned_by":"upstream","display_name":"Upstream Name","description":"upstream description","context_window":42}]}`)

	enriched, err := catalog.EnrichModelsListBody(context.Background(), body, PlatformOpenAI, nil)
	require.NoError(t, err)
	require.Contains(t, string(enriched), `"description":"upstream description"`)
	require.Contains(t, string(enriched), `"display_name":"Upstream Name"`)
	require.Contains(t, string(enriched), `"context_window":42`)
}

func TestModelMetadataCatalogEnrichWithoutRegistryIsSafe(t *testing.T) {
	catalog := newTestModelMetadataCatalogWithClient(nil)
	catalog.mu.Lock()
	catalog.lastAttempt = time.Now() // suppress the background fetch
	catalog.mu.Unlock()

	body := []byte(`{"object":"list","data":[{"id":"deepseek-v4-pro","object":"model","created":0,"owned_by":"openai","type":"model","display_name":"deepseek-v4-pro"}]}`)
	enriched, err := catalog.EnrichModelsListBody(context.Background(), body, PlatformOpenAI, nil)
	require.NoError(t, err)

	var result struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(enriched, &result))
	require.Len(t, result.Data, 1)
	require.Equal(t, "deepseek-v4-pro", result.Data[0]["id"])
	require.Equal(t, "DeepSeek V4 Pro", result.Data[0]["display_name"])
	require.Equal(t, "model", result.Data[0]["object"])
	require.Equal(t, "openai", result.Data[0]["owned_by"])
}

func TestModelMetadataCatalogEnrichRejectsMalformedBodyWithoutPanic(t *testing.T) {
	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	body := []byte(`{"object":"list","data":{}}`)
	enriched, err := catalog.EnrichModelsListBody(context.Background(), body, PlatformOpenAI, nil)
	require.NoError(t, err)
	require.Equal(t, string(body), string(enriched), "non-array data is returned unchanged")
}

func TestModelMetadataCatalogSyncFetchesRegistry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"deepseek":{"id":"deepseek","name":"DeepSeek","models":{"deepseek-v4.1-flash":{"id":"deepseek-v4.1-flash","name":"DeepSeek V4.1 Flash","limit":{"context":1048576,"output":384000},"cost":{"input":0.165,"output":0.66}}}}}`)
	}))
	defer server.Close()

	catalog := newTestModelMetadataCatalogWithClient(server.Client())
	catalog.registryURL = server.URL
	result, err := catalog.Sync(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.Providers)
	require.Equal(t, 1, result.Models)
	require.NotEmpty(t, result.FetchedAt)

	metadata := catalog.Resolve(context.Background(), "deepseek-v4.1-flash", PlatformOpenAI, nil)
	require.Equal(t, ModelMetadataSourceModelsDev, metadata.Source)
	require.Equal(t, int64(1_048_576), metadata.ContextWindow)
}

func TestModelMetadataCatalogSyncFailureKeepsLastSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	catalog := newTestModelMetadataCatalog(testModelsDevRegistry())
	catalog.registryURL = server.URL
	_, err := catalog.Sync(context.Background())
	require.Error(t, err)

	providers, models, fetchedAt := catalog.RegistryStats()
	require.Equal(t, len(testModelsDevRegistry()), providers)
	require.Positive(t, models)
	require.False(t, fetchedAt.IsZero())
	require.Equal(t, "DeepSeek V4.1 Flash", catalog.Resolve(context.Background(), "deepseek-v4.1-flash", PlatformOpenAI, nil).DisplayName)
}

func TestModelMetadataCatalogSyncHonorsContextTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	catalog := newTestModelMetadataCatalogWithClient(server.Client())
	catalog.registryURL = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := catalog.Sync(ctx)
	require.Error(t, err)
}

func TestModelMetadataCatalogResolveDoesNotBlockOnRegistryFetch(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = io.WriteString(w, `{}`)
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	catalog := newTestModelMetadataCatalogWithClient(server.Client())
	catalog.registryURL = server.URL
	started := time.Now()
	metadata := catalog.Resolve(context.Background(), "deepseek-v4-pro", PlatformOpenAI, nil)
	require.Less(t, time.Since(started), time.Second, "resolving must not wait for the registry")
	require.Equal(t, "DeepSeek V4 Pro", metadata.DisplayName)
}

// K2.7-pro and K2.8 are not in the models.dev registry yet; the builtin
// catalogue must still describe them instead of falling back to a bare id.
func TestBuiltinCatalogMetadataCoversKimiK27ProAndK28(t *testing.T) {
	for _, modelID := range []string{"kimi-k2.7-pro", "kimi-k2.8"} {
		entry, ok := builtinCatalogModelMetadata(modelID)
		require.True(t, ok, modelID)
		require.NotEmpty(t, entry.DisplayName, modelID)
		require.NotEmpty(t, entry.Description, modelID)
		require.Equal(t, int64(262144), entry.ContextWindow, modelID)
		require.Equal(t, int64(65536), entry.MaxOutputTokens, modelID)
		require.NotNil(t, entry.ToolCall, modelID)
		require.True(t, *entry.ToolCall, modelID)
		require.NotNil(t, entry.Reasoning, modelID)
		require.True(t, entry.Authoritative, modelID)
	}
	_, ok := builtinCatalogModelMetadata("kimi-k9-unlisted")
	require.False(t, ok)
}

// models.dev can carry a reseller copy of a DeepSeek ID whose limits were
// rounded or forwarded (1050K context, output = context). First-party values
// verified against api-docs.deepseek.com are authoritative and must win, while
// the registry cost stays a reference price.
func TestModelMetadataCatalogAuthoritativeDeepSeekOverridesResellerLimits(t *testing.T) {
	registry := map[string]modelsDevProvider{
		"reseller": {
			ID:   "reseller",
			Name: "Reseller",
			Models: map[string]modelsDevModel{
				"deepseek-v4.1-flash-1017": {
					ID:          "deepseek-v4.1-flash-1017",
					Name:        "Reseller DeepSeek Flash",
					Description: "Reseller copy with forwarded limits.",
					Reasoning:   testBoolPointer(false),
					ToolCall:    testBoolPointer(false),
					Modalities:  modelsDevModalities{Input: []string{"text"}, Output: []string{"text"}},
					Limit:       modelsDevLimit{Context: 1_050_000, Output: 1_050_000},
					Cost:        modelsDevCost{Input: testFloatPointer(9.99)},
				},
				"deepseek-v4-pro-0813": {
					ID:         "deepseek-v4-pro-0813",
					Name:       "Reseller DeepSeek Pro",
					Reasoning:  testBoolPointer(false),
					ToolCall:   testBoolPointer(false),
					Modalities: modelsDevModalities{Input: []string{"text", "image"}, Output: []string{"text"}},
					Limit:      modelsDevLimit{Context: 1_050_000, Output: 1_050_000},
				},
			},
		},
	}
	catalog := newTestModelMetadataCatalog(registry)
	ctx := context.Background()

	flash := catalog.Resolve(ctx, "deepseek-v4.1-flash-1017", PlatformOpenAI, nil)
	require.Equal(t, ModelMetadataSourceModelsDev, flash.Source, "the registry still matched; only the values are overridden")
	require.Equal(t, "DeepSeek V4.1 Flash 1017", flash.DisplayName)
	require.Equal(t, "Reseller copy with forwarded limits.", flash.Description, "registry description is preserved")
	require.Equal(t, int64(1_048_576), flash.ContextWindow)
	require.Equal(t, int64(393_216), flash.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, flash.InputModalities, "flash understands images")
	require.Equal(t, []string{"text"}, flash.OutputModalities)
	require.Equal(t, "deepseek-v4.1", flash.Family)
	require.NotNil(t, flash.Capabilities)
	require.True(t, flash.Capabilities.Reasoning)
	require.True(t, flash.Capabilities.ToolCall)
	require.NotNil(t, flash.ReferencePricing, "models.dev cost stays a reference price")
	require.InDelta(t, 9.99, *flash.ReferencePricing.Input, 1e-9)
	require.Nil(t, flash.Pricing, "no price card was configured")

	pro := catalog.Resolve(ctx, "deepseek-v4-pro-0813", PlatformOpenAI, nil)
	require.Equal(t, "DeepSeek V4 Pro 0813", pro.DisplayName)
	require.Equal(t, int64(1_048_576), pro.ContextWindow)
	require.Equal(t, int64(393_216), pro.MaxOutputTokens)
	require.Equal(t, []string{"text"}, pro.InputModalities, "Pro has no vision support")
	require.Equal(t, "deepseek-v4-pro", pro.Family)
	require.True(t, pro.Capabilities.Reasoning)

	// The primary alias has no registry entry at all; the built-in entry alone
	// must still describe it completely.
	alias := catalog.Resolve(ctx, "deepseek-flash", PlatformOpenAI, nil)
	require.Equal(t, ModelMetadataSourceBuiltin, alias.Source)
	require.Equal(t, "DeepSeek V4.1 Flash", alias.DisplayName)
	require.Equal(t, int64(1_048_576), alias.ContextWindow)
	require.Equal(t, int64(393_216), alias.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, alias.InputModalities)
	require.NotNil(t, alias.Capabilities)
	require.True(t, alias.Capabilities.Reasoning)
	require.True(t, alias.Capabilities.ToolCall)
}

// Dotted aliases (claude-opus-4.5) must reach the dashed registry ID
// (claude-opus-4-5); without the rewrite the plaza falls back to a bare name.
func TestModelMetadataCatalogDottedAliasMatchesDashedRegistryID(t *testing.T) {
	registry := map[string]modelsDevProvider{
		"anthropic": {
			ID: "anthropic",
			Models: map[string]modelsDevModel{
				"claude-opus-4-5": {
					ID:               "claude-opus-4-5",
					Name:             "Claude Opus 4.5",
					Reasoning:        testBoolPointer(true),
					ToolCall:         testBoolPointer(true),
					Modalities:       modelsDevModalities{Input: []string{"text", "image"}, Output: []string{"text"}},
					Limit:            modelsDevLimit{Context: 1_000_000, Output: 128_000},
					Family:           "claude-opus",
					StructuredOutput: testBoolPointer(true),
				},
			},
		},
	}
	catalog := newTestModelMetadataCatalog(registry)
	metadata := catalog.Resolve(context.Background(), "claude-opus-4.5", PlatformAnthropic, nil)
	require.Equal(t, ModelMetadataSourceModelsDev, metadata.Source)
	require.Equal(t, "Claude Opus 4.5", metadata.DisplayName)
	require.Equal(t, int64(1_000_000), metadata.ContextWindow)
	require.Equal(t, int64(128_000), metadata.MaxOutputTokens)
	require.Equal(t, []string{"text", "image"}, metadata.InputModalities)
	require.NotNil(t, metadata.Capabilities)
	require.True(t, metadata.Capabilities.Reasoning)
	require.True(t, metadata.Capabilities.ToolCall)
}

// Kimi K2.7-pro / K2.8 are still absent from models.dev; a same-name reseller
// listing must not shadow the built-in K2 envelope.
func TestModelMetadataCatalogAuthoritativeKimiOverridesReseller(t *testing.T) {
	registry := map[string]modelsDevProvider{
		"reseller": {
			ID: "reseller",
			Models: map[string]modelsDevModel{
				"kimi-k2.7-pro": {
					ID:         "kimi-k2.7-pro",
					Name:       "Reseller Kimi K2.7 Pro",
					Reasoning:  testBoolPointer(false),
					ToolCall:   testBoolPointer(false),
					Modalities: modelsDevModalities{Input: []string{"text"}, Output: []string{"text"}},
					Limit:      modelsDevLimit{Context: 1_050_000, Output: 1_050_000},
				},
				"kimi-k2.8": {
					ID:         "kimi-k2.8",
					Name:       "Reseller Kimi K2.8",
					Reasoning:  testBoolPointer(false),
					Modalities: modelsDevModalities{Input: []string{"text"}, Output: []string{"text"}},
					Limit:      modelsDevLimit{Context: 1_050_000, Output: 1_050_000},
				},
			},
		},
	}
	catalog := newTestModelMetadataCatalog(registry)
	ctx := context.Background()

	cases := []struct {
		id      string
		display string
		family  string
	}{
		{"kimi-k2.7-pro", "Kimi K2.7 Pro", "kimi-k2.7"},
		{"kimi-k2.8", "Kimi K2.8", "kimi-k2.8"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			metadata := catalog.Resolve(ctx, tc.id, PlatformOpenAI, nil)
			require.Equal(t, tc.display, metadata.DisplayName)
			require.Equal(t, int64(256*1024), metadata.ContextWindow)
			require.Equal(t, int64(65_536), metadata.MaxOutputTokens)
			require.Equal(t, []string{"text", "image"}, metadata.InputModalities)
			require.Equal(t, []string{"text"}, metadata.OutputModalities)
			require.Equal(t, tc.family, metadata.Family)
			require.NotNil(t, metadata.Capabilities)
			require.True(t, metadata.Capabilities.Reasoning)
			require.True(t, metadata.Capabilities.ToolCall)
		})
	}
}
