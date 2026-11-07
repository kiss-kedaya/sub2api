package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Model metadata catalogue used by public model discovery (/v1/models).
//
// Sources are layered so that nothing is invented:
//  1. the configured price card (ChannelModelPricing) for billed prices;
//  2. the models.dev registry for descriptive metadata (exact match first,
//     then a normalized match, then a family fallback);
//  3. built-in first-party capability metadata for known model families;
//  4. a readable display name derived from the model ID.
//
// The registry is refreshed in the background: a model-list request never
// blocks on models.dev and a failed refresh keeps serving the last good
// snapshot. Only the admin sync endpoint forces a synchronous refresh.
const (
	// ModelMetadataSourceModelsDev marks metadata read from the models.dev registry.
	ModelMetadataSourceModelsDev = "models.dev"
	// ModelMetadataSourceBuiltin marks metadata from the built-in capability tables.
	ModelMetadataSourceBuiltin = "builtin"
	// ModelMetadataSourceFamily marks metadata borrowed from a sibling model.
	ModelMetadataSourceFamily = "family"
	// ModelMetadataSourceNone marks entries that only carry a generated display name.
	ModelMetadataSourceNone = "none"

	modelCatalogPricingUnit = "USD/1M tokens"
	modelCatalogRequestUnit = "USD/request"

	modelMetadataRegistryBodyLimit int64 = 16 << 20
	modelMetadataRegistryTimeout         = 20 * time.Second
	// modelMetadataRegistryRetryDelay bounds how often a failing registry fetch is
	// retried from the hot path, so an outage cannot turn into a per-request fetch.
	modelMetadataRegistryRetryDelay = 2 * time.Minute
)

// errModelMetadataCatalogUnavailable is returned when the gateway has no
// metadata catalogue (for example a partially constructed service in tests).
var errModelMetadataCatalogUnavailable = fmt.Errorf("model metadata catalog is not configured")

// ModelCatalogPricing is a USD per one-million-token price block.
type ModelCatalogPricing struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	Unit       string   `json:"unit"`
}

// ModelCatalogRequestPricing is a per-request price block (image/per-call models).
type ModelCatalogRequestPricing struct {
	PerRequest *float64 `json:"per_request"`
	Unit       string   `json:"unit"`
}

// ModelCapabilities is the boolean capability matrix exposed by /v1/models.
type ModelCapabilities struct {
	Reasoning        bool `json:"reasoning"`
	ToolCall         bool `json:"tool_call"`
	StructuredOutput bool `json:"structured_output"`
	Attachment       bool `json:"attachment"`
}

// ModelMetadata is the catalogue view of a single public model ID.
type ModelMetadata struct {
	ID               string                      `json:"id"`
	DisplayName      string                      `json:"display_name"`
	Description      string                      `json:"description,omitempty"`
	ContextWindow    int64                       `json:"context_window,omitempty"`
	MaxOutputTokens  int64                       `json:"max_output_tokens,omitempty"`
	InputModalities  []string                    `json:"input_modalities,omitempty"`
	OutputModalities []string                    `json:"output_modalities,omitempty"`
	Capabilities     *ModelCapabilities          `json:"capabilities,omitempty"`
	Pricing          *ModelCatalogPricing        `json:"pricing,omitempty"`
	RequestPricing   *ModelCatalogRequestPricing `json:"per_request_pricing,omitempty"`
	ReferencePricing *ModelCatalogPricing        `json:"reference_pricing,omitempty"`
	Family           string                      `json:"family,omitempty"`
	ReleaseDate      string                      `json:"release_date,omitempty"`
	Knowledge        string                      `json:"knowledge,omitempty"`
	OpenWeights      *bool                       `json:"open_weights,omitempty"`
	Source           string                      `json:"metadata_source,omitempty"`
}

// ModelPricingResolverFunc resolves the billed price card for one model ID.
// Group-level cards win over channel cards, matching the billing resolver.
type ModelPricingResolverFunc func(modelID string) *ChannelModelPricing

// ModelMetadataSyncResult is the admin refresh response.
type ModelMetadataSyncResult struct {
	Providers int    `json:"providers"`
	Models    int    `json:"models"`
	FetchedAt string `json:"fetched_at"`
}

type modelsDevModelIndexEntry struct {
	ProviderID string
	Model      modelsDevModel
}

// FindConfiguredModelPricing matches a model ID against a configured price card
// list (exact first, then wildcard) for one platform. The platform-independent
// wrapper keeps the billing matcher as the single source of truth.
func FindConfiguredModelPricing(pricing []ChannelModelPricing, platform, modelID string) *ChannelModelPricing {
	return findPricingForModel(pricing, platform, strings.ToLower(strings.TrimSpace(modelID)))
}

// ModelMetadataCatalog resolves model IDs against the models.dev registry and
// the configured price cards. It is safe for concurrent use.
type ModelMetadataCatalog struct {
	client      *http.Client
	now         func() time.Time
	registryURL string

	mu            sync.RWMutex
	registry      map[string]modelsDevProvider
	index         map[string][]modelsDevModelIndexEntry
	familyIndex   map[string][]modelsDevModelIndexEntry
	fetchedAt     time.Time
	lastAttempt   time.Time
	lastErr       string
	providerCount int
	modelCount    int

	fetchMu   sync.Mutex
	fetching  bool
	fetchOnce singleflight.Group
}

// NewModelMetadataCatalog builds a catalogue with an optional HTTP client. A nil
// client uses the process default transport with a bounded timeout.
func NewModelMetadataCatalog(client *http.Client) *ModelMetadataCatalog {
	if client == nil {
		client = &http.Client{Timeout: modelMetadataRegistryTimeout}
	}
	return &ModelMetadataCatalog{
		client:      client,
		now:         time.Now,
		registryURL: modelsDevRegistryURL,
		index:       map[string][]modelsDevModelIndexEntry{},
		familyIndex: map[string][]modelsDevModelIndexEntry{},
	}
}

func (c *ModelMetadataCatalog) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
}

// SeedRegistry installs a registry snapshot without a network call. It exists
// for tests and for callers that already fetched the registry.
func (c *ModelMetadataCatalog) SeedRegistry(registry map[string]modelsDevProvider, fetchedAt time.Time) {
	if c == nil {
		return
	}
	index, familyIndex, providers, models := buildModelMetadataIndex(registry)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registry = registry
	c.index = index
	c.familyIndex = familyIndex
	c.providerCount = providers
	c.modelCount = models
	c.fetchedAt = fetchedAt
	c.lastErr = ""
}

// LastError reports the most recent registry fetch failure, if any. It is empty
// after a successful refresh and is exposed for admin diagnostics.
func (c *ModelMetadataCatalog) LastError() string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

// RegistryStats reports the current snapshot size and fetch time.
func (c *ModelMetadataCatalog) RegistryStats() (providers, models int, fetchedAt time.Time) {
	if c == nil {
		return 0, 0, time.Time{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.providerCount, c.modelCount, c.fetchedAt
}

// Sync fetches the models.dev registry and reports what was loaded. It never
// mutates prices or billing configuration.
func (c *ModelMetadataCatalog) Sync(ctx context.Context) (*ModelMetadataSyncResult, error) {
	if c == nil {
		return nil, fmt.Errorf("model metadata catalog is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	registry, err := c.fetchRegistry(ctx)
	if err != nil {
		c.mu.Lock()
		c.lastErr = err.Error()
		c.mu.Unlock()
		return nil, err
	}
	c.SeedRegistry(registry, c.clock())
	providers, models, fetchedAt := c.RegistryStats()
	return &ModelMetadataSyncResult{
		Providers: providers,
		Models:    models,
		FetchedAt: fetchedAt.UTC().Format(time.RFC3339),
	}, nil
}

func (c *ModelMetadataCatalog) fetchRegistry(ctx context.Context) (map[string]modelsDevProvider, error) {
	value, err, _ := c.fetchOnce.Do("models.dev", func() (any, error) {
		reqCtx := ctx
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			reqCtx, cancel = context.WithTimeout(ctx, modelMetadataRegistryTimeout)
			defer cancel()
		}
		registryURL := c.registryURL
		if strings.TrimSpace(registryURL) == "" {
			registryURL = modelsDevRegistryURL
		}
		req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, registryURL, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Accept", "application/json")
		client := c.client
		if client == nil {
			client = &http.Client{Timeout: modelMetadataRegistryTimeout}
		}
		resp, doErr := client.Do(req)
		if doErr != nil {
			return nil, fmt.Errorf("model metadata registry request failed: %w", doErr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, fmt.Errorf("model metadata registry returned HTTP %d", resp.StatusCode)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, modelMetadataRegistryBodyLimit+1))
		if readErr != nil {
			return nil, fmt.Errorf("read model metadata registry: %w", readErr)
		}
		if int64(len(body)) > modelMetadataRegistryBodyLimit {
			return nil, fmt.Errorf("model metadata registry response exceeds %d bytes", modelMetadataRegistryBodyLimit)
		}
		var registry map[string]modelsDevProvider
		if err := json.Unmarshal(body, &registry); err != nil {
			return nil, fmt.Errorf("parse model metadata registry: %w", err)
		}
		if len(registry) == 0 {
			return nil, fmt.Errorf("model metadata registry is empty")
		}
		return registry, nil
	})
	if err != nil {
		return nil, err
	}
	registry, _ := value.(map[string]modelsDevProvider)
	if len(registry) == 0 {
		return nil, fmt.Errorf("model metadata registry is empty")
	}
	return registry, nil
}

// ensureRegistryAsync refreshes a stale or missing snapshot without blocking the
// caller. Failures are logged once per attempt and retried after a delay.
func (c *ModelMetadataCatalog) ensureRegistryAsync(ctx context.Context) {
	if c == nil {
		return
	}
	now := c.clock()
	c.mu.RLock()
	fresh := len(c.registry) > 0 && now.Sub(c.fetchedAt) < modelsDevRegistryTTL
	attempted := !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < modelMetadataRegistryRetryDelay
	c.mu.RUnlock()
	if fresh || attempted {
		return
	}

	c.fetchMu.Lock()
	if c.fetching {
		c.fetchMu.Unlock()
		return
	}
	c.fetching = true
	c.mu.Lock()
	c.lastAttempt = now
	c.mu.Unlock()
	c.fetchMu.Unlock()

	go func() {
		defer func() {
			c.fetchMu.Lock()
			c.fetching = false
			c.fetchMu.Unlock()
		}()
		fetchCtx := context.Background()
		if ctx != nil {
			fetchCtx = context.WithoutCancel(ctx)
		}
		registry, err := c.fetchRegistry(fetchCtx)
		if err != nil {
			c.mu.Lock()
			c.lastErr = err.Error()
			c.mu.Unlock()
			slog.Warn("model metadata registry refresh failed", "error", err)
			return
		}
		c.SeedRegistry(registry, c.clock())
	}()
}

// Resolve returns catalogue metadata for one public model ID. platform is the
// requesting platform hint and resolvePricing supplies the billed price card;
// billed prices always win over the registry's reference prices.
func (c *ModelMetadataCatalog) Resolve(ctx context.Context, modelID, platform string, resolvePricing ModelPricingResolverFunc) ModelMetadata {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ModelMetadata{Source: ModelMetadataSourceNone}
	}
	if c != nil {
		c.ensureRegistryAsync(ctx)
	}
	var index, familyIndex map[string][]modelsDevModelIndexEntry
	if c != nil {
		c.mu.RLock()
		index, familyIndex = c.index, c.familyIndex
		c.mu.RUnlock()
	}
	return resolveModelMetadata(index, familyIndex, modelID, platform, resolvePricing)
}

// EnrichModelsListBody merges catalogue metadata into a model-list body shaped
// as {"data":[...]} (OpenAI/Claude/Grok) or {"models":[...]} (Codex manifest).
// Entries that already carry a field keep it; catalogue values only fill gaps.
// A resolution or parse failure returns the original body unchanged so that
// /v1/models can never fail because of the metadata source.
func (c *ModelMetadataCatalog) EnrichModelsListBody(ctx context.Context, body []byte, platform string, resolvePricing ModelPricingResolverFunc) ([]byte, error) {
	if c == nil || len(body) == 0 {
		return body, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, err
	}
	field := "data"
	raw := bytes.TrimSpace(envelope[field])
	if len(raw) == 0 || raw[0] != '[' {
		field = "models"
		raw = bytes.TrimSpace(envelope[field])
	}
	if len(raw) == 0 || raw[0] != '[' {
		return body, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return body, err
	}
	enriched := make([]json.RawMessage, 0, len(entries))
	for _, rawEntry := range entries {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(rawEntry, &entry); err != nil || entry == nil {
			enriched = append(enriched, rawEntry)
			continue
		}
		id := jsonStringField(entry["id"])
		if id == "" {
			id = jsonStringField(entry["slug"])
		}
		if strings.TrimSpace(id) == "" {
			enriched = append(enriched, rawEntry)
			continue
		}
		metadata := c.Resolve(ctx, id, platform, resolvePricing)
		applyModelMetadataToEntry(entry, id, metadata)
		encoded, err := json.Marshal(entry)
		if err != nil {
			return body, err
		}
		enriched = append(enriched, encoded)
	}
	encodedEntries, err := json.Marshal(enriched)
	if err != nil {
		return body, err
	}
	envelope[field] = encodedEntries
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return body, err
	}
	return encoded, nil
}

// applyModelMetadataToEntry writes catalogue fields into a raw model entry.
// Existing upstream fields are authoritative and are never overwritten; the
// configured price is the exception because billed prices must match billing.
func applyModelMetadataToEntry(entry map[string]json.RawMessage, id string, metadata ModelMetadata) {
	if entry == nil {
		return
	}
	if metadata.DisplayName != "" {
		existing := strings.TrimSpace(jsonStringField(entry["display_name"]))
		if existing == "" || strings.EqualFold(existing, strings.TrimSpace(id)) {
			setJSONField(entry, "display_name", metadata.DisplayName)
		}
	}
	setJSONFieldIfAbsent(entry, "description", metadata.Description)
	if metadata.ContextWindow > 0 && jsonInt64Field(entry["context_window"]) <= 0 {
		setJSONField(entry, "context_window", metadata.ContextWindow)
	}
	if metadata.MaxOutputTokens > 0 && jsonInt64Field(entry["max_output_tokens"]) <= 0 {
		setJSONField(entry, "max_output_tokens", metadata.MaxOutputTokens)
	}
	if len(metadata.InputModalities) > 0 && len(jsonStringSliceField(entry["input_modalities"])) == 0 {
		setJSONField(entry, "input_modalities", metadata.InputModalities)
	}
	if len(metadata.OutputModalities) > 0 && len(jsonStringSliceField(entry["output_modalities"])) == 0 {
		setJSONField(entry, "output_modalities", metadata.OutputModalities)
	}
	if metadata.Capabilities != nil && len(entry["capabilities"]) == 0 {
		setJSONField(entry, "capabilities", metadata.Capabilities)
	}
	if metadata.Pricing != nil {
		setJSONField(entry, "pricing", metadata.Pricing)
	}
	if metadata.RequestPricing != nil && len(entry["per_request_pricing"]) == 0 {
		setJSONField(entry, "per_request_pricing", metadata.RequestPricing)
	}
	if metadata.ReferencePricing != nil && len(entry["reference_pricing"]) == 0 {
		setJSONField(entry, "reference_pricing", metadata.ReferencePricing)
	}
	setJSONFieldIfAbsent(entry, "family", metadata.Family)
	setJSONFieldIfAbsent(entry, "release_date", metadata.ReleaseDate)
	setJSONFieldIfAbsent(entry, "knowledge", metadata.Knowledge)
	if metadata.OpenWeights != nil && len(entry["open_weights"]) == 0 {
		setJSONField(entry, "open_weights", *metadata.OpenWeights)
	}
}

func setJSONField(entry map[string]json.RawMessage, key string, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	entry[key] = encoded
}

func setJSONFieldIfAbsent(entry map[string]json.RawMessage, key string, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	if strings.TrimSpace(jsonStringField(entry[key])) != "" {
		return
	}
	setJSONField(entry, key, value)
}

func jsonStringField(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func jsonInt64Field(raw json.RawMessage) int64 {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0
	}
	return value
}

func jsonStringSliceField(raw json.RawMessage) []string {
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

// resolveModelMetadata implements the lookup ladder. It is pure so tests can
// exercise it with a seeded index.
func resolveModelMetadata(
	index map[string][]modelsDevModelIndexEntry,
	familyIndex map[string][]modelsDevModelIndexEntry,
	modelID, platform string,
	resolvePricing ModelPricingResolverFunc,
) ModelMetadata {
	metadata := ModelMetadata{ID: modelID, Source: ModelMetadataSourceNone}
	lowerID := strings.ToLower(modelID)

	var entry modelsDevModelIndexEntry
	source := ""
	if candidates := index[lowerID]; len(candidates) > 0 {
		entry, source = candidates[0], ModelMetadataSourceModelsDev
	}
	if source == "" {
		for _, normalized := range normalizedModelIdentifiers(lowerID) {
			if candidates := index[normalized]; len(candidates) > 0 {
				entry, source = candidates[0], ModelMetadataSourceModelsDev
				break
			}
		}
	}
	// Family fallback 1: drop trailing segments until a registry model matches.
	// The sibling is the same family, so descriptive fields may be borrowed.
	if source == "" {
		for _, candidateID := range modelPrefixCandidates(lowerID) {
			if candidates := index[candidateID]; len(candidates) > 0 {
				entry, source = candidates[0], ModelMetadataSourceFamily
				break
			}
		}
	}
	// Family fallback 2: registry family key (e.g. "gpt-image"). Only the shape
	// (modalities, capabilities, family) is safe to borrow here.
	shapeOnly := false
	if source == "" {
		for _, candidateID := range modelPrefixCandidates(lowerID) {
			if candidates := familyIndex[candidateID]; len(candidates) > 0 {
				entry, source, shapeOnly = candidates[0], ModelMetadataSourceFamily, true
				break
			}
		}
	}

	if source != "" {
		applyModelsDevModel(&metadata, entry.Model, source, shapeOnly)
	}

	// Built-in first-party knowledge fills what the registry omits and is the
	// display-name anchor when the registry has no entry at all.
	if builtin, ok := builtinModelCatalogMetadata(modelID); ok {
		mergeBuiltinModelMetadata(&metadata, builtin)
		if metadata.Source == ModelMetadataSourceNone {
			metadata.Source = ModelMetadataSourceBuiltin
		}
	}

	if strings.TrimSpace(metadata.DisplayName) == "" || strings.EqualFold(strings.TrimSpace(metadata.DisplayName), strings.TrimSpace(modelID)) {
		// Registries sometimes echo the raw ID as the display name; a generated
		// readable name is friendlier for clients that render it directly.
		if derived := readableModelDisplayName(modelID); derived != "" {
			metadata.DisplayName = derived
		}
	}

	// Billed prices come from the configured price card and always win. The
	// registry price stays in reference_pricing; it is never promoted to the
	// billed price because it is not the amount the gateway charges.
	metadata.Pricing, metadata.RequestPricing = resolveConfiguredModelPricing(resolvePricing, modelID)
	return metadata
}

func applyModelsDevModel(metadata *ModelMetadata, model modelsDevModel, source string, shapeOnly bool) {
	if metadata == nil {
		return
	}
	metadata.Source = source
	if !shapeOnly {
		if name := strings.TrimSpace(model.Name); name != "" {
			metadata.DisplayName = name
		}
		metadata.Description = strings.TrimSpace(model.Description)
		metadata.ContextWindow = model.Limit.Context
		metadata.MaxOutputTokens = model.Limit.Output
		metadata.ReleaseDate = strings.TrimSpace(model.ReleaseDate)
		metadata.Knowledge = strings.TrimSpace(model.Knowledge)
		// A family approximation must not borrow a sibling's price: only a real
		// registry hit for this ID may surface as reference price.
		if source == ModelMetadataSourceModelsDev {
			metadata.ReferencePricing = modelsDevModelPricing(model.Cost)
		}
	}
	metadata.InputModalities = normalizeModelModalities(model.Modalities.Input)
	metadata.OutputModalities = normalizeModelModalities(model.Modalities.Output)
	metadata.Capabilities = modelsDevModelCapabilities(model)
	metadata.Family = strings.TrimSpace(model.Family)
	metadata.OpenWeights = copyBoolPointer(model.OpenWeights)
}

func modelsDevModelCapabilities(model modelsDevModel) *ModelCapabilities {
	if model.Reasoning == nil && model.ToolCall == nil && model.StructuredOutput == nil && model.Attachment == nil {
		return nil
	}
	capabilities := &ModelCapabilities{}
	if model.Reasoning != nil {
		capabilities.Reasoning = *model.Reasoning
	}
	if model.ToolCall != nil {
		capabilities.ToolCall = *model.ToolCall
	}
	if model.StructuredOutput != nil {
		capabilities.StructuredOutput = *model.StructuredOutput
	}
	if model.Attachment != nil {
		capabilities.Attachment = *model.Attachment
	}
	return capabilities
}

func modelsDevModelPricing(cost modelsDevCost) *ModelCatalogPricing {
	pricing := &ModelCatalogPricing{Unit: modelCatalogPricingUnit}
	if cost.Input != nil {
		pricing.Input = copyFloatPointer(cost.Input)
	}
	if cost.Output != nil {
		pricing.Output = copyFloatPointer(cost.Output)
	}
	if cost.CacheRead != nil {
		pricing.CacheRead = copyFloatPointer(cost.CacheRead)
	}
	if cost.CacheWrite != nil {
		pricing.CacheWrite = copyFloatPointer(cost.CacheWrite)
	}
	if pricing.Input == nil && pricing.Output == nil && pricing.CacheRead == nil && pricing.CacheWrite == nil {
		return nil
	}
	return pricing
}

// resolveConfiguredModelPricing converts the configured per-token price card
// into the USD/1M-token representation used by the public catalogue.
func resolveConfiguredModelPricing(resolvePricing ModelPricingResolverFunc, modelID string) (*ModelCatalogPricing, *ModelCatalogRequestPricing) {
	if resolvePricing == nil {
		return nil, nil
	}
	matched := resolvePricing(modelID)
	if matched == nil {
		return nil, nil
	}
	token := &ModelCatalogPricing{Unit: modelCatalogPricingUnit}
	token.Input = perTokenToPerMillion(matched.InputPrice)
	token.Output = perTokenToPerMillion(matched.OutputPrice)
	token.CacheRead = perTokenToPerMillion(matched.CacheReadPrice)
	token.CacheWrite = perTokenToPerMillion(matched.CacheWritePrice)
	if token.Input == nil && token.Output == nil && token.CacheRead == nil && token.CacheWrite == nil {
		token = nil
	}
	var request *ModelCatalogRequestPricing
	if matched.PerRequestPrice != nil {
		request = &ModelCatalogRequestPricing{PerRequest: copyFloatPointer(matched.PerRequestPrice), Unit: modelCatalogRequestUnit}
	}
	return token, request
}

func perTokenToPerMillion(perToken *float64) *float64 {
	if perToken == nil || *perToken < 0 {
		return nil
	}
	value := math.Round(*perToken*1_000_000*1_000_000) / 1_000_000
	return &value
}

func copyFloatPointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func copyBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func normalizeModelModalities(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildModelMetadataIndex flattens the provider registry into ID and family
// indexes. Candidate order is deterministic: first-party providers first, then
// entries that actually carry a price, then the largest context window.
func buildModelMetadataIndex(registry map[string]modelsDevProvider) (map[string][]modelsDevModelIndexEntry, map[string][]modelsDevModelIndexEntry, int, int) {
	providers := make([]string, 0, len(registry))
	for providerID := range registry {
		providers = append(providers, providerID)
	}
	sort.Slice(providers, func(i, j int) bool {
		left, right := modelsDevProviderRank(providers[i]), modelsDevProviderRank(providers[j])
		if left != right {
			return left < right
		}
		return providers[i] < providers[j]
	})

	index := make(map[string][]modelsDevModelIndexEntry)
	familyIndex := make(map[string][]modelsDevModelIndexEntry)
	modelCount := 0
	for _, providerID := range providers {
		provider := registry[providerID]
		for key, model := range provider.Models {
			modelCount++
			entry := modelsDevModelIndexEntry{ProviderID: providerID, Model: model}
			for _, alias := range modelsDevModelAliases(key, model) {
				index[alias] = append(index[alias], entry)
			}
			if family := strings.ToLower(strings.TrimSpace(model.Family)); family != "" {
				familyIndex[family] = append(familyIndex[family], entry)
			}
		}
	}
	for key := range index {
		index[key] = rankModelsDevCandidates(index[key])
	}
	for key := range familyIndex {
		familyIndex[key] = rankModelsDevCandidates(familyIndex[key])
	}
	return index, familyIndex, len(registry), modelCount
}

func modelsDevModelAliases(key string, model modelsDevModel) []string {
	aliases := make([]string, 0, 3)
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		for _, existing := range aliases {
			if existing == value {
				return
			}
		}
		aliases = append(aliases, value)
	}
	add(key)
	add(model.ID)
	// canonical_model_id is "<vendor>/<model>"; the model part is the public alias.
	if canonical := strings.TrimSpace(model.CanonicalModelID); canonical != "" {
		if slash := strings.LastIndexByte(canonical, '/'); slash >= 0 {
			add(canonical[slash+1:])
		} else {
			add(canonical)
		}
	}
	return aliases
}

func rankModelsDevCandidates(entries []modelsDevModelIndexEntry) []modelsDevModelIndexEntry {
	if len(entries) < 2 {
		return entries
	}
	ranked := append([]modelsDevModelIndexEntry(nil), entries...)
	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := scoreModelsDevCandidate(ranked[i]), scoreModelsDevCandidate(ranked[j])
		if left.rank != right.rank {
			return left.rank < right.rank
		}
		if left.hasCost != right.hasCost {
			return left.hasCost
		}
		if left.context != right.context {
			return left.context > right.context
		}
		return left.providerID < right.providerID
	})
	return ranked
}

type modelsDevCandidateScore struct {
	rank       int
	hasCost    bool
	context    int64
	providerID string
}

func scoreModelsDevCandidate(entry modelsDevModelIndexEntry) modelsDevCandidateScore {
	cost := entry.Model.Cost
	return modelsDevCandidateScore{
		rank:       modelsDevProviderRank(entry.ProviderID),
		hasCost:    cost.Input != nil || cost.Output != nil,
		context:    entry.Model.Limit.Context,
		providerID: entry.ProviderID,
	}
}

// modelsDevProviderRank prefers first-party catalogues over resellers so that a
// model ID resolves to the vendor's own entry when several providers list it.
func modelsDevProviderRank(providerID string) int {
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "openai", "anthropic", "google", "deepseek", "xai", "zhipuai", "moonshotai", "moonshotai-cn", "alibaba", "qwen", "minimax":
		return 0
	case "azure", "azure-cognitive-services", "google-vertex", "amazon-bedrock", "vertex":
		return 10
	default:
		return 20
	}
}

// normalizedModelIdentifiers produces progressively looser spellings of a model
// ID: vendor prefix removed, release/expiry suffixes removed, variant suffixes
// removed, and dotted versions flattened ("gpt-image-2-6" -> "gpt-image-2.6").
func normalizedModelIdentifiers(modelID string) []string {
	base := strings.ToLower(strings.TrimSpace(modelID))
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		base = strings.TrimSpace(base[slash+1:])
	}
	if base == "" {
		return nil
	}
	candidates := make([]string, 0, 6)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}

	trimmed := stripModelIDSuffixes(base)
	add(trimmed)
	add(stripVendorPrefix(trimmed))
	for _, dotted := range flattenDottedVersion(trimmed) {
		add(dotted)
		add(stripVendorPrefix(dotted))
	}
	for _, dashed := range dashedVersionSpelling(trimmed) {
		add(dashed)
		add(stripVendorPrefix(dashed))
	}
	return candidates
}

var modelIDVariantSuffixes = []string{
	"-expires-on",
	"-latest",
	"-xhigh",
	"-non-thinking",
	"-non-reasoning",
	"-thinking",
	"-reasoning",
	"-minimal",
	"-medium",
	"-high",
	"-low",
	"-max",
}

// stripModelIDSuffixes removes release dates, "-expires-on-*" markers and
// effort/variant suffixes, repeatedly, so "m-high-0731" settles on "m".
func stripModelIDSuffixes(modelID string) string {
	value := strings.TrimSpace(modelID)
	for {
		previous := value
		value = strings.TrimRight(value, "-")
		if trimmed := trimTrailingReleaseDigits(value); trimmed != value {
			value = trimmed
		}
		for _, suffix := range modelIDVariantSuffixes {
			if strings.HasSuffix(value, suffix) {
				value = strings.TrimSuffix(value, suffix)
				if suffix == "-expires-on" {
					value = trimTrailingReleaseDigits(value)
				}
				break
			}
		}
		if value == previous {
			return value
		}
	}
}

// trimTrailingReleaseDigits removes a numeric release token ("-0731", "-20250910").
func trimTrailingReleaseDigits(modelID string) string {
	index := strings.LastIndexByte(modelID, '-')
	if index < 0 {
		return modelID
	}
	token := modelID[index+1:]
	if len(token) != 4 && len(token) != 6 && len(token) != 8 {
		return modelID
	}
	for _, char := range token {
		if char < '0' || char > '9' {
			return modelID
		}
	}
	return modelID[:index]
}

func stripVendorPrefix(modelID string) string {
	tokens := strings.Split(modelID, "-")
	if len(tokens) < 2 {
		return modelID
	}
	switch tokens[0] {
	case "openai", "anthropic", "google", "deepseek", "xai", "zhipuai", "moonshotai", "moonshot", "alibaba", "qwen", "meta", "mistral", "cohere":
		return strings.Join(tokens[1:], "-")
	default:
		return modelID
	}
}

// flattenDottedVersion appends a dotted-version spelling for IDs that encode a
// version with a dash ("gpt-image-2-6" -> "gpt-image-2.6").
func flattenDottedVersion(modelID string) []string {
	tokens := strings.Split(modelID, "-")
	if len(tokens) < 3 {
		return nil
	}
	last := tokens[len(tokens)-1]
	previous := tokens[len(tokens)-2]
	if !isNumericToken(previous) || !isNumericToken(last) {
		return nil
	}
	flattened := strings.Join(tokens[:len(tokens)-2], "-") + "-" + previous + "." + last
	return []string{flattened}
}

// dashedVersionSpelling appends a dashed-version spelling for IDs that encode a
// trailing numeric version with a dot ("claude-opus-4.5" -> "claude-opus-4-5").
// The registry and the vendor APIs spell that model with a dash, so the dotted
// alias used by some clients would otherwise miss the registry entry entirely.
func dashedVersionSpelling(modelID string) []string {
	dash := strings.LastIndexByte(modelID, '-')
	if dash < 0 || dash == len(modelID)-1 {
		return nil
	}
	token := modelID[dash+1:]
	if !strings.Contains(token, ".") || !isNumericToken(strings.ReplaceAll(token, ".", "")) {
		return nil
	}
	return []string{modelID[:dash+1] + strings.ReplaceAll(token, ".", "-")}
}

func isNumericToken(token string) bool {
	if token == "" {
		return false
	}
	for _, char := range token {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// modelPrefixCandidates yields the requested ID minus its trailing segments,
// longest first, skipping the bare vendor token.
func modelPrefixCandidates(modelID string) []string {
	base := stripModelIDSuffixes(modelID)
	tokens := strings.Split(base, "-")
	candidates := make([]string, 0, len(tokens))
	for length := len(tokens) - 1; length >= 2; length-- {
		candidates = append(candidates, strings.Join(tokens[:length], "-"))
	}
	return candidates
}

// builtinModelCatalogMetadata reuses the first-party capability tables defined
// for upstream sync (DeepSeek, GPT Image, Grok Imagine, Gemini tiered).
func builtinModelCatalogMetadata(modelID string) (UpstreamModelMetadata, bool) {
	if entry, ok := builtinDeepSeekModelMetadata(modelID); ok {
		return entry, true
	}
	return builtinCatalogModelMetadata(modelID)
}

// mergeBuiltinModelMetadata folds first-party knowledge into catalogue metadata.
//
// A models.dev hit normally wins because it is the broadest source; a built-in
// entry only fills the fields the registry omits. Entries marked Authoritative
// are the exception: their values were verified against the vendor's own docs,
// so they replace what a registry (often a reseller copy: 1050K context, output
// equal to context) published. Reference prices are never touched — models.dev
// cost stays in reference_pricing, billed prices stay on the configured card.
func mergeBuiltinModelMetadata(metadata *ModelMetadata, builtin UpstreamModelMetadata) {
	if metadata == nil {
		return
	}
	if builtin.Authoritative {
		overrideModelMetadataWithBuiltin(metadata, builtin)
		return
	}
	if metadata.Source != ModelMetadataSourceModelsDev && strings.TrimSpace(builtin.DisplayName) != "" {
		metadata.DisplayName = strings.TrimSpace(builtin.DisplayName)
	}
	if metadata.Description == "" {
		metadata.Description = strings.TrimSpace(builtin.Description)
	}
	if metadata.ContextWindow <= 0 && builtin.ContextWindow > 0 {
		metadata.ContextWindow = builtin.ContextWindow
	}
	if metadata.MaxOutputTokens <= 0 && builtin.MaxOutputTokens > 0 {
		metadata.MaxOutputTokens = builtin.MaxOutputTokens
	}
	if len(metadata.InputModalities) == 0 && len(builtin.InputModalities) > 0 {
		metadata.InputModalities = append([]string(nil), builtin.InputModalities...)
	}
	if metadata.Capabilities == nil && builtin.Reasoning != nil {
		metadata.Capabilities = &ModelCapabilities{Reasoning: *builtin.Reasoning}
	}
}

// overrideModelMetadataWithBuiltin applies first-party values over a registry
// hit. Only the fields the vendor documents are overridden (name, context,
// output, modalities, reasoning/tool-call, family); registry-only facts such as
// description, release date, open weights and reference pricing are preserved.
func overrideModelMetadataWithBuiltin(metadata *ModelMetadata, builtin UpstreamModelMetadata) {
	if name := strings.TrimSpace(builtin.DisplayName); name != "" {
		metadata.DisplayName = name
	}
	if metadata.Description == "" {
		metadata.Description = strings.TrimSpace(builtin.Description)
	}
	if builtin.ContextWindow > 0 {
		metadata.ContextWindow = builtin.ContextWindow
	}
	if builtin.MaxOutputTokens > 0 {
		metadata.MaxOutputTokens = builtin.MaxOutputTokens
	}
	if len(builtin.InputModalities) > 0 {
		metadata.InputModalities = append([]string(nil), builtin.InputModalities...)
	}
	if len(builtin.OutputModalities) > 0 {
		metadata.OutputModalities = append([]string(nil), builtin.OutputModalities...)
	}
	if family := strings.TrimSpace(builtin.Family); family != "" {
		metadata.Family = family
	}
	if builtin.Reasoning != nil || builtin.ToolCall != nil {
		capabilities := ModelCapabilities{}
		if metadata.Capabilities != nil {
			capabilities = *metadata.Capabilities
		}
		if builtin.Reasoning != nil {
			capabilities.Reasoning = *builtin.Reasoning
		}
		if builtin.ToolCall != nil {
			capabilities.ToolCall = *builtin.ToolCall
		}
		metadata.Capabilities = &capabilities
	}
	if metadata.ReleaseDate == "" {
		metadata.ReleaseDate = strings.TrimSpace(builtin.ReleaseDate)
	}
}

var modelDisplayNameVendors = map[string]string{
	"gpt":      "GPT",
	"glm":      "GLM",
	"deepseek": "DeepSeek",
	"kimi":     "Kimi",
	"grok":     "Grok",
	"gemini":   "Gemini",
	"claude":   "Claude",
	"qwen":     "Qwen",
	"ai":       "AI",
	"tts":      "TTS",
	"stt":      "STT",
	"mcp":      "MCP",
}

// readableModelDisplayName builds a human-readable name from a raw model ID,
// used when no registry or built-in entry exists.
func readableModelDisplayName(modelID string) string {
	trimmed := strings.TrimSpace(modelID)
	if trimmed == "" {
		return ""
	}
	if slash := strings.LastIndexByte(trimmed, '/'); slash >= 0 && slash < len(trimmed)-1 {
		trimmed = trimmed[slash+1:]
	}
	tokens := strings.Split(trimmed, "-")
	words := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if named, ok := modelDisplayNameVendors[strings.ToLower(token)]; ok {
			words = append(words, named)
			continue
		}
		if isNumericToken(token) {
			words = append(words, token)
			continue
		}
		if strings.ContainsAny(token, "0123456789.") && isNumericToken(strings.ReplaceAll(token, ".", "")) {
			words = append(words, token)
			continue
		}
		words = append(words, strings.ToUpper(token[:1])+token[1:])
	}
	return strings.Join(words, " ")
}
