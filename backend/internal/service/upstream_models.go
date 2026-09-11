package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const (
	upstreamModelsBodyLimit             int64 = 8 << 20
	modelsDevRegistryURL                      = "https://models.dev/api.json"
	modelsDevRegistryTTL                      = 6 * time.Hour
	UpstreamModelMetadataExtraKey             = "upstream_model_metadata"
	UpstreamModelMetadataIncompleteCode       = "upstream_model_metadata_incomplete"
	UpstreamModelMetadataPartialCode          = "upstream_model_metadata_partial"
)

type UpstreamModelMetadata struct {
	ID                       string                     `json:"id"`
	DisplayName              string                     `json:"display_name,omitempty"`
	Description              string                     `json:"description,omitempty"`
	Reasoning                *bool                      `json:"reasoning,omitempty"`
	DefaultReasoningLevel    string                     `json:"default_reasoning_level,omitempty"`
	SupportedReasoningLevels []string                   `json:"supported_reasoning_levels,omitempty"`
	InputModalities          []string                   `json:"input_modalities,omitempty"`
	ContextWindow            int64                      `json:"context_window,omitempty"`
	MaxOutputTokens          int64                      `json:"max_output_tokens,omitempty"`
	CodexToolCapabilities    map[string]json.RawMessage `json:"codex_tool_capabilities,omitempty"`
}

type UpstreamModelMetadataSnapshot struct {
	Source   string                           `json:"source"`
	SyncedAt string                           `json:"synced_at"`
	Models   map[string]UpstreamModelMetadata `json:"models"`
}

type UpstreamModelCatalog struct {
	Models   []string                         `json:"models"`
	Metadata map[string]UpstreamModelMetadata `json:"metadata,omitempty"`
	Warnings []UpstreamModelSyncWarning       `json:"warnings,omitempty"`
}

type UpstreamModelSyncWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	API    string                    `json:"api"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Reasoning        *bool                      `json:"reasoning"`
	ReasoningOptions []modelsDevReasoningOption `json:"reasoning_options"`
	Modalities       modelsDevModalities        `json:"modalities"`
	Limit            modelsDevLimit             `json:"limit"`
}

type modelsDevReasoningOption struct {
	Type   string `json:"type"`
	Values []any  `json:"values"`
}

type modelsDevModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type modelsDevLimit struct {
	Context int64 `json:"context"`
	Output  int64 `json:"output"`
}

func (a *Account) SetUpstreamModelMetadataSnapshot(snapshot UpstreamModelMetadataSnapshot) {
	if a == nil {
		return
	}
	if a.Extra == nil {
		a.Extra = make(map[string]any)
	}
	a.Extra[UpstreamModelMetadataExtraKey] = snapshot
}

func (a *Account) GetUpstreamModelMetadataSnapshot() *UpstreamModelMetadataSnapshot {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[UpstreamModelMetadataExtraKey]
	if !ok || raw == nil {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var snapshot UpstreamModelMetadataSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil || len(snapshot.Models) == 0 {
		return nil
	}
	return &snapshot
}

func (a *Account) GetUpstreamModelMetadata(modelID string) (UpstreamModelMetadata, bool) {
	snapshot := a.GetUpstreamModelMetadataSnapshot()
	if snapshot == nil {
		return UpstreamModelMetadata{}, false
	}
	metadata, ok := snapshot.Models[strings.TrimSpace(modelID)]
	return metadata, ok
}

// UpstreamModelSyncErrorKind classifies model sync failures for safe HTTP mapping.
type UpstreamModelSyncErrorKind string

const (
	// UpstreamModelSyncErrorConfiguration means the account or server configuration cannot perform the sync.
	UpstreamModelSyncErrorConfiguration UpstreamModelSyncErrorKind = "configuration"
	// UpstreamModelSyncErrorUnsupported means the account format is intentionally unsupported for live model sync.
	UpstreamModelSyncErrorUnsupported UpstreamModelSyncErrorKind = "unsupported"
	// UpstreamModelSyncErrorUpstream means the configured upstream failed or returned an unusable response.
	UpstreamModelSyncErrorUpstream UpstreamModelSyncErrorKind = "upstream"
	// UpstreamModelSyncErrorInternal means local persistence or service state failed after a valid upstream response.
	UpstreamModelSyncErrorInternal UpstreamModelSyncErrorKind = "internal"
)

// UpstreamModelSyncError keeps internal failure details wrapped while exposing a safe client message.
type UpstreamModelSyncError struct {
	Kind       UpstreamModelSyncErrorKind
	Message    string
	StatusCode int
	Err        error
}

func (e *UpstreamModelSyncError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Message
	}
	return e.Message + ": " + e.Err.Error()
}

func (e *UpstreamModelSyncError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SafeMessage returns the sanitized message that can be sent to API clients.
func (e *UpstreamModelSyncError) SafeMessage() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "Failed to sync upstream models"
	}
	return e.Message
}

func newUpstreamModelSyncConfigError(message string, err error) error {
	return &UpstreamModelSyncError{Kind: UpstreamModelSyncErrorConfiguration, Message: message, Err: err}
}

func newUpstreamModelSyncUnsupportedError(message string, err error) error {
	return &UpstreamModelSyncError{Kind: UpstreamModelSyncErrorUnsupported, Message: message, Err: err}
}

func newUpstreamModelSyncUpstreamError(message string, err error) error {
	return &UpstreamModelSyncError{Kind: UpstreamModelSyncErrorUpstream, Message: message, Err: err}
}

func newUpstreamModelSyncInternalError(message string, err error) error {
	return &UpstreamModelSyncError{Kind: UpstreamModelSyncErrorInternal, Message: message, Err: err}
}

// FetchUpstreamSupportedModels fetches only live model IDs. The admin sync path
// uses SyncUpstreamModelCatalog so capability metadata can also be persisted.
func (s *AccountTestService) FetchUpstreamSupportedModels(ctx context.Context, account *Account) ([]string, error) {
	models, _, err := s.fetchUpstreamModelList(ctx, account)
	return models, err
}

// SyncUpstreamModelCatalog fetches the account's live model list, enriches
// missing capability fields from the provider registry used by the upstream,
// and persists a normalized account snapshot when complete metadata is available.
//
// Persistence is per-model: models with complete capability fields are saved even
// when other IDs in the same sync remain incomplete. An incomplete warning is
// still returned so admins can tell ID sync succeeded without a full capability
// snapshot. When no model is complete, the existing account snapshot is left
// untouched.
func (s *AccountTestService) SyncUpstreamModelCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	models, body, err := s.fetchUpstreamModelList(ctx, account)
	liveListAvailable := err == nil
	if err != nil {
		configuredModels := configuredUpstreamModelsForCapabilitySync(account)
		if !upstreamModelListEndpointUnsupported(err) || len(configuredModels) == 0 {
			return nil, err
		}
		models = configuredModels
		body = nil
		slog.Info("upstream model list endpoint unavailable; using configured models for capability sync",
			"account_id", upstreamModelSyncAccountID(account),
			"platform", upstreamModelSyncPlatform(account),
			"status_code", upstreamModelSyncStatusCode(err),
			"model_count", len(models),
		)
	}
	if filtered := filterUpstreamModelsByAccountPlatform(account, models); filtered != nil && len(filtered) != len(models) {
		slog.Info("dropped cross-platform models from upstream sync",
			"account_id", upstreamModelSyncAccountID(account),
			"platform", upstreamModelSyncPlatform(account),
			"before", len(models),
			"after", len(filtered),
		)
		models = filtered
	}
	catalog := &UpstreamModelCatalog{Models: models, Metadata: make(map[string]UpstreamModelMetadata)}
	if len(body) > 0 {
		_, directMetadata, parseErr := extractUpstreamModelCatalog(body, account != nil && account.IsGrok())
		if parseErr == nil {
			catalog.Metadata = directMetadata
		}
	}

	// Capability enrichment also covers concrete model_mapping targets. Admins may
	// whitelist models that the live /models list omitted; those still need registry
	// metadata so Codex catalogs can advertise reasoning and modalities.
	enrichIDs := dedupeAndSortModelIDs(append(append([]string{}, models...), configuredUpstreamModelsForCapabilitySync(account)...))
	// Dedicated image/video generators are not Codex agent catalog entries and often
	// omit context windows in public registries. Keep them out of completeness checks
	// so they do not mask successful agent-model capability sync.
	capabilityIDs := capabilitySyncModelIDs(enrichIDs)

	source := "upstream"
	if upstreamCatalogNeedsRegistry(capabilityIDs, catalog.Metadata) {
		if registryMetadata, registryErr := s.fetchModelsDevMetadata(ctx, account, enrichIDs); registryErr == nil {
			for modelID, fallback := range registryMetadata {
				current := catalog.Metadata[modelID]
				merged, changed := mergeUpstreamModelMetadata(current, fallback)
				catalog.Metadata[modelID] = merged
				if changed {
					source = "models.dev"
				}
			}
		} else {
			slog.Warn("upstream model capability metadata enrichment failed",
				"account_id", upstreamModelSyncAccountID(account),
				"platform", upstreamModelSyncPlatform(account),
				"error", registryErr,
			)
		}
	}
	if builtin := builtinDeepSeekModelMetadataForIDs(enrichIDs); len(builtin) > 0 {
		for modelID, fallback := range builtin {
			current := catalog.Metadata[modelID]
			merged, changed := mergeUpstreamModelMetadata(current, fallback)
			catalog.Metadata[modelID] = merged
			if changed && source == "upstream" {
				source = "builtin"
			}
		}
	}
	if builtin := builtinCatalogModelMetadataForIDs(enrichIDs); len(builtin) > 0 {
		for modelID, fallback := range builtin {
			current := catalog.Metadata[modelID]
			merged, changed := mergeUpstreamModelMetadata(current, fallback)
			catalog.Metadata[modelID] = merged
			if changed && source == "upstream" {
				source = "builtin"
			}
		}
	}
	inheritUpstreamModelVariantMetadata(catalog.Metadata, enrichIDs)

	completeMetadata := completeUpstreamModelMetadataSubset(capabilityIDs, catalog.Metadata)
	persistedCapabilities := false
	if len(completeMetadata) > 0 && account != nil && account.ID > 0 && s.accountRepo != nil {
		// Retain known metadata only for models still listed or explicitly mapped.
		if previous := account.GetUpstreamModelMetadataSnapshot(); previous != nil {
			retainedModels := capabilityIDs
			if !liveListAvailable {
				retainedModels = append([]string(nil), capabilityIDs...)
				for modelID := range previous.Models {
					retainedModels = append(retainedModels, modelID)
				}
			}
			for _, modelID := range retainedModels {
				old, exists := previous.Models[modelID]
				if !exists {
					continue
				}
				if entry, ok := completeMetadata[modelID]; ok {
					if entry.CodexToolCapabilities == nil {
						entry.CodexToolCapabilities = make(map[string]json.RawMessage)
					}
					applyCodexToolCapabilities(entry.CodexToolCapabilities, old.CodexToolCapabilities, false)
					completeMetadata[modelID] = entry
				} else {
					completeMetadata[modelID] = old
				}
			}
		}
		snapshot := UpstreamModelMetadataSnapshot{
			Source:   source,
			SyncedAt: time.Now().UTC().Format(time.RFC3339),
			Models:   completeMetadata,
		}
		if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{UpstreamModelMetadataExtraKey: snapshot}); err != nil {
			return nil, newUpstreamModelSyncInternalError("Failed to save upstream model metadata", err)
		}
		account.SetUpstreamModelMetadataSnapshot(snapshot)
		persistedCapabilities = true
	}

	if upstreamCatalogNeedsRegistry(capabilityIDs, catalog.Metadata) {
		if persistedCapabilities {
			catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
				Code:    UpstreamModelMetadataPartialCode,
				Message: "Some model capabilities were saved; remaining models are still incomplete.",
			})
		} else {
			catalog.Warnings = append(catalog.Warnings, UpstreamModelSyncWarning{
				Code:    UpstreamModelMetadataIncompleteCode,
				Message: "Model IDs were synced, but capability metadata is incomplete.",
			})
		}
	}
	return catalog, nil
}

func upstreamModelSyncStatusCode(err error) int {
	var syncErr *UpstreamModelSyncError
	if errors.As(err, &syncErr) {
		return syncErr.StatusCode
	}
	return 0
}

func upstreamModelListEndpointUnsupported(err error) bool {
	statusCode := upstreamModelSyncStatusCode(err)
	return statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed
}

func configuredUpstreamModelsForCapabilitySync(account *Account) []string {
	if account == nil {
		return nil
	}
	models := make([]string, 0)
	for _, mappedModel := range account.GetModelMapping() {
		mappedModel = strings.TrimSpace(mappedModel)
		if mappedModel == "" || strings.Contains(mappedModel, "*") {
			continue
		}
		models = append(models, mappedModel)
	}
	return dedupeAndSortModelIDs(models)
}

func capabilitySyncModelIDs(modelIDs []string) []string {
	filtered := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || isCodexDedicatedMediaModel(modelID) {
			continue
		}
		filtered = append(filtered, modelID)
	}
	return filtered
}

func upstreamModelSyncAccountID(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}

func upstreamModelSyncPlatform(account *Account) string {
	if account == nil {
		return ""
	}
	return account.Platform
}

func upstreamCatalogNeedsRegistry(models []string, metadata map[string]UpstreamModelMetadata) bool {
	for _, modelID := range models {
		modelID = strings.TrimSpace(modelID)
		model, ok := metadata[modelID]
		if !ok || !upstreamModelMetadataIsComplete(model) {
			return true
		}
	}
	return false
}

func upstreamModelMetadataIsUseful(metadata UpstreamModelMetadata) bool {
	return strings.TrimSpace(metadata.DisplayName) != "" ||
		strings.TrimSpace(metadata.Description) != "" ||
		metadata.Reasoning != nil ||
		len(metadata.SupportedReasoningLevels) > 0 ||
		len(metadata.InputModalities) > 0 ||
		len(metadata.CodexToolCapabilities) > 0 ||
		metadata.ContextWindow > 0 ||
		metadata.MaxOutputTokens > 0
}

// upstreamModelMetadataIsComplete reports whether a snapshot entry is safe to
// persist and later prefer over local Codex name-based fallbacks.
func upstreamModelMetadataIsComplete(metadata UpstreamModelMetadata) bool {
	if metadata.Reasoning == nil {
		return false
	}
	if len(normalizeCodexInputModalities(metadata.InputModalities)) == 0 {
		return false
	}
	if metadata.ContextWindow <= 0 {
		return false
	}
	if *metadata.Reasoning && len(normalizeReasoningLevels(metadata.SupportedReasoningLevels)) == 0 {
		return false
	}
	return true
}

func completeUpstreamModelMetadataSubset(
	modelIDs []string,
	metadata map[string]UpstreamModelMetadata,
) map[string]UpstreamModelMetadata {
	if len(metadata) == 0 {
		return nil
	}
	complete := make(map[string]UpstreamModelMetadata)
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		entry, ok := metadata[modelID]
		if !ok || !upstreamModelMetadataIsComplete(entry) {
			continue
		}
		if strings.TrimSpace(entry.ID) == "" {
			entry.ID = modelID
		}
		complete[modelID] = entry
	}
	if len(complete) == 0 {
		return nil
	}
	return complete
}

func mergeUpstreamModelMetadata(primary, fallback UpstreamModelMetadata) (UpstreamModelMetadata, bool) {
	merged := primary
	changed := false
	if strings.TrimSpace(merged.ID) == "" && strings.TrimSpace(fallback.ID) != "" {
		merged.ID = strings.TrimSpace(fallback.ID)
		changed = true
	}
	if strings.TrimSpace(merged.DisplayName) == "" && strings.TrimSpace(fallback.DisplayName) != "" {
		merged.DisplayName = strings.TrimSpace(fallback.DisplayName)
		changed = true
	}
	if strings.TrimSpace(merged.Description) == "" && strings.TrimSpace(fallback.Description) != "" {
		merged.Description = strings.TrimSpace(fallback.Description)
		changed = true
	}
	if merged.Reasoning == nil && fallback.Reasoning != nil {
		reasoning := *fallback.Reasoning
		merged.Reasoning = &reasoning
		changed = true
	}
	if strings.TrimSpace(merged.DefaultReasoningLevel) == "" && strings.TrimSpace(fallback.DefaultReasoningLevel) != "" {
		merged.DefaultReasoningLevel = strings.TrimSpace(fallback.DefaultReasoningLevel)
		changed = true
	}
	if len(merged.SupportedReasoningLevels) == 0 && len(fallback.SupportedReasoningLevels) > 0 {
		merged.SupportedReasoningLevels = append([]string(nil), fallback.SupportedReasoningLevels...)
		changed = true
	}
	if len(merged.InputModalities) == 0 && len(fallback.InputModalities) > 0 {
		merged.InputModalities = append([]string(nil), fallback.InputModalities...)
		changed = true
	}
	if merged.ContextWindow <= 0 && fallback.ContextWindow > 0 {
		merged.ContextWindow = fallback.ContextWindow
		changed = true
	}
	if merged.MaxOutputTokens <= 0 && fallback.MaxOutputTokens > 0 {
		merged.MaxOutputTokens = fallback.MaxOutputTokens
		changed = true
	}
	return merged, changed
}

func (s *AccountTestService) fetchModelsDevMetadata(
	ctx context.Context,
	account *Account,
	modelIDs []string,
) (map[string]UpstreamModelMetadata, error) {
	if s == nil || s.httpUpstream == nil || account == nil {
		return nil, fmt.Errorf("model metadata registry is not configured")
	}
	registry, err := s.fetchModelsDevRegistry(ctx, account)
	if err != nil {
		return nil, err
	}
	metadata := make(map[string]UpstreamModelMetadata)
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		model, found := lookupModelsDevModel(registry, account, modelID)
		if !found {
			continue
		}
		entry := upstreamMetadataFromModelsDevModel(modelID, model)
		if upstreamModelMetadataIsUseful(entry) {
			metadata[modelID] = entry
		}
	}
	if len(metadata) == 0 {
		return nil, fmt.Errorf("no model metadata provider matches account base URL or model IDs")
	}
	return metadata, nil
}

func (s *AccountTestService) fetchModelsDevRegistry(ctx context.Context, account *Account) (map[string]modelsDevProvider, error) {
	now := time.Now()
	s.modelMetadataRegistryMu.Lock()
	if len(s.modelMetadataRegistry) > 0 && now.Sub(s.modelMetadataRegistryAt) < modelsDevRegistryTTL {
		cached := s.modelMetadataRegistry
		s.modelMetadataRegistryMu.Unlock()
		return cached, nil
	}
	s.modelMetadataRegistryMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevRegistryURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.doUpstreamModelsRequest(req, upstreamModelsProxyURL(account), account)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("model metadata registry returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelsBodyLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > upstreamModelsBodyLimit {
		return nil, fmt.Errorf("model metadata registry response exceeds %d bytes", upstreamModelsBodyLimit)
	}
	var registry map[string]modelsDevProvider
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("parse model metadata registry: %w", err)
	}
	if len(registry) == 0 {
		return nil, fmt.Errorf("model metadata registry is empty")
	}

	s.modelMetadataRegistryMu.Lock()
	s.modelMetadataRegistry = registry
	s.modelMetadataRegistryAt = now
	s.modelMetadataRegistryMu.Unlock()
	return registry, nil
}

func upstreamMetadataFromModelsDevModel(modelID string, model modelsDevModel) UpstreamModelMetadata {
	levels := reasoningLevelsFromModelsDevOptions(model.ReasoningOptions)
	reasoning := model.Reasoning
	if reasoning == nil && len(levels) > 0 {
		inferred := true
		reasoning = &inferred
	}
	metadata := UpstreamModelMetadata{
		ID:                       strings.TrimSpace(modelID),
		DisplayName:              strings.TrimSpace(model.Name),
		Description:              strings.TrimSpace(model.Description),
		Reasoning:                reasoning,
		SupportedReasoningLevels: levels,
		InputModalities:          normalizeCodexInputModalities(model.Modalities.Input),
		ContextWindow:            model.Limit.Context,
		MaxOutputTokens:          model.Limit.Output,
	}
	if len(levels) > 0 {
		metadata.DefaultReasoningLevel = levels[0]
	}
	if strings.TrimSpace(model.ID) != "" {
		metadata.ID = strings.TrimSpace(model.ID)
	}
	return metadata
}

func reasoningLevelsFromModelsDevOptions(options []modelsDevReasoningOption) []string {
	levels := make([]string, 0)
	for _, option := range options {
		if !strings.EqualFold(strings.TrimSpace(option.Type), "effort") {
			continue
		}
		for _, value := range option.Values {
			if value == nil {
				levels = append(levels, "none")
				continue
			}
			if effort, ok := value.(string); ok {
				levels = append(levels, effort)
			}
		}
	}
	return normalizeReasoningLevels(levels)
}

func upstreamModelRegistryBaseURL(account *Account) string {
	if account == nil {
		return ""
	}
	switch {
	case account.IsOpenAI() || account.IsCNProvider():
		return account.GetOpenAIFormatBaseURL()
	case account.IsGrok():
		return account.GetGrokBaseURL()
	case account.IsGemini():
		return account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
	case account.IsAnthropic():
		return account.GetBaseURL()
	case account.Platform == PlatformAntigravity:
		return account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
	default:
		return strings.TrimSpace(account.GetCredential("base_url"))
	}
}

const (
	deepSeekV4ContextWindow int64 = 1_048_576
	deepSeekV4MaxOutput     int64 = 384_000
)

func builtinDeepSeekModelMetadataForIDs(modelIDs []string) map[string]UpstreamModelMetadata {
	out := make(map[string]UpstreamModelMetadata)
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if entry, ok := builtinDeepSeekModelMetadata(modelID); ok {
			out[modelID] = entry
		}
	}
	return out
}

// builtinDeepSeekModelMetadata fills capability fields DeepSeek /models omits
// and models.dev does not yet list for dated V4 IDs.
// Official: deepseek-flash = V4.1-Flash (vision); deepseek-v4-pro = V4-Pro-0813 (no vision).
// Old flash IDs stay callable but are served by V4.1-Flash. Thinking default high; levels none/low/high/max.
func builtinDeepSeekModelMetadata(modelID string) (UpstreamModelMetadata, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	id = strings.TrimPrefix(id, "deepseek/")
	if id == "" {
		return UpstreamModelMetadata{}, false
	}
	if id != "deepseek-flash" && !strings.HasPrefix(id, "deepseek-v4") {
		return UpstreamModelMetadata{}, false
	}

	display := "DeepSeek V4"
	switch {
	case id == "deepseek-v4-flash-0731":
		display = "DeepSeek V4 Flash 0731"
	case id == "deepseek-v4-flash-vision-exp":
		display = "DeepSeek V4 Flash Vision Exp"
	case id == "deepseek-v4-pro-0813":
		display = "DeepSeek V4 Pro 0813"
	case id == "deepseek-v4.1-flash-0910":
		display = "DeepSeek V4.1 Flash 0910"
	case id == "deepseek-v4-flash":
		display = "DeepSeek V4 Flash"
	case id == "deepseek-v4.1-flash" || id == "deepseek-flash":
		display = "DeepSeek V4.1 Flash"
	case strings.Contains(id, "pro"):
		display = "DeepSeek V4 Pro"
	case strings.Contains(id, "vision"):
		display = "DeepSeek V4 Flash Vision"
	case strings.Contains(id, "flash"):
		display = "DeepSeek V4 Flash"
	}

	// Pro has no vision. Flash family (including dated/old aliases) is served by V4.1-Flash.
	vision := !strings.Contains(id, "pro")

	reasoning := true
	modalities := []string{"text"}
	if vision {
		modalities = append(modalities, "image")
	}
	return UpstreamModelMetadata{
		ID:                       strings.TrimSpace(modelID),
		DisplayName:              display,
		Description:              deepSeekV4Description(id, vision),
		Reasoning:                &reasoning,
		DefaultReasoningLevel:    "high",
		SupportedReasoningLevels: []string{"none", "low", "high", "max"},
		InputModalities:          modalities,
		ContextWindow:            deepSeekV4ContextWindow,
		MaxOutputTokens:          deepSeekV4MaxOutput,
	}, true
}

func deepSeekV4Description(id string, vision bool) string {
	switch {
	case strings.Contains(id, "pro"):
		return "DeepSeek V4 Pro with million-token context and support for thinking and non-thinking modes. Official /models omits capability fields; vision is not supported."
	case strings.Contains(id, "vision"):
		return "Experimental multimodal DeepSeek V4 Flash model for image understanding, coding, and agentic work. Official docs route this ID to DeepSeek-V4.1-Flash."
	case strings.Contains(id, "v4.1") || id == "deepseek-flash":
		return "DeepSeek-V4.1-Flash with 1M context, 384K max output, thinking/non-thinking modes, and image understanding. Official model name is deepseek-flash."
	case vision:
		return "DeepSeek V4 Flash family served by DeepSeek-V4.1-Flash: 1M context, 384K max output, thinking/non-thinking modes, and image understanding."
	default:
		return "DeepSeek V4 model with million-token context and support for thinking and non-thinking modes."
	}
}

func builtinCatalogModelMetadataForIDs(modelIDs []string) map[string]UpstreamModelMetadata {
	out := make(map[string]UpstreamModelMetadata)
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if entry, ok := builtinCatalogModelMetadata(modelID); ok {
			out[modelID] = entry
		}
	}
	return out
}

// builtinCatalogModelMetadata fills official capability fields that /models and
// models.dev omit for Codex probes and media generators.
func builtinCatalogModelMetadata(modelID string) (UpstreamModelMetadata, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	id = strings.TrimPrefix(id, "openai/")
	id = strings.TrimPrefix(id, "anthropic/")
	id = strings.TrimPrefix(id, "xai/")
	id = strings.TrimPrefix(id, "google/")
	if slash := strings.LastIndexByte(id, '/'); slash >= 0 {
		id = strings.TrimSpace(id[slash+1:])
	}
	if id == "" {
		return UpstreamModelMetadata{}, false
	}

	switch {
	case id == openai.CodexUsageProbeModel:
		reasoning := true
		return UpstreamModelMetadata{
			ID:                       strings.TrimSpace(modelID),
			DisplayName:              "Codex Auto Review",
			Description:              "Automatic approval review model for Codex.",
			Reasoning:                &reasoning,
			DefaultReasoningLevel:    "medium",
			SupportedReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
			InputModalities:          []string{"text", "image"},
			ContextWindow:            272000,
		}, true
	case IsGPTImageGenerationModel(id):
		reasoning := false
		return UpstreamModelMetadata{
			ID:              strings.TrimSpace(modelID),
			DisplayName:     gptImageCatalogDisplayName(id),
			Description:     gptImageCatalogDescription(id),
			Reasoning:       &reasoning,
			InputModalities: []string{"text", "image"},
			ContextWindow:   16384,
			MaxOutputTokens: 16384,
		}, true
	case xai.IsGrokImagineModel(id):
		reasoning := false
		return UpstreamModelMetadata{
			ID:              strings.TrimSpace(modelID),
			DisplayName:     grokImagineCatalogDisplayName(id),
			Description:     grokImagineCatalogDescription(id),
			Reasoning:       &reasoning,
			InputModalities: []string{"text", "image"},
			ContextWindow:   grokImagineCatalogContextWindow(id),
			MaxOutputTokens: grokImagineCatalogMaxOutput(id),
		}, true
	default:
		return UpstreamModelMetadata{}, false
	}
}

func gptImageCatalogDisplayName(id string) string {
	switch {
	case strings.HasPrefix(id, "gpt-image-2.5-sunburst"):
		return "GPT Image 2.5 Sunburst"
	case strings.HasPrefix(id, "gpt-image-2.5-flare"):
		return "GPT Image 2.5 Flare"
	case strings.HasPrefix(id, "gpt-image-2.5"):
		return "GPT Image 2.5"
	case strings.HasPrefix(id, "gpt-image-2-exact"):
		return "GPT Image 2 Exact"
	case strings.HasPrefix(id, "gpt-image-1k-th"):
		return "GPT Image 1K TH"
	case strings.HasPrefix(id, "gpt-image-1.5"):
		return "GPT Image 1.5"
	case strings.HasPrefix(id, "gpt-image-1"):
		return "GPT Image 1"
	default:
		return "GPT Image 2"
	}
}

func gptImageCatalogDescription(id string) string {
	switch {
	case strings.HasPrefix(id, "gpt-image-2.5-sunburst"):
		return "GPT Image 2.5 Sunburst generates and edits images from text and image inputs. Use it for workflows where editing precision matters most."
	case strings.HasPrefix(id, "gpt-image-2.5-flare"):
		return "GPT Image 2.5 Flare is OpenAI's fastest GPT Image 2.5 model for high-quality, everyday image generation and editing."
	case strings.HasPrefix(id, "gpt-image-2.5"):
		return "GPT Image 2.5 family for prompt-driven generation and precise editing. Select flare for speed or sunburst for quality."
	case strings.HasPrefix(id, "gpt-image-2-exact"):
		return "GPT Image 2 exact-edit variant for prompt-driven generation and visual design workflows."
	case strings.HasPrefix(id, "gpt-image-1k-th"):
		return "GPT Image 1K thumbnail variant for prompt-driven generation and visual design workflows."
	default:
		return "Image model for prompt-driven generation, editing, and visual design workflows."
	}
}

func grokImagineCatalogDisplayName(id string) string {
	switch {
	case strings.Contains(id, "video-1.5"):
		return "Grok Imagine Video 1.5"
	case strings.Contains(id, "video"):
		return "Grok Imagine Video"
	case strings.Contains(id, "image-2.0"):
		return "Grok Imagine Image 2.0"
	case strings.Contains(id, "quality"):
		return "Grok Imagine Image Quality"
	default:
		return "Grok Imagine Image"
	}
}

func grokImagineCatalogDescription(id string) string {
	if strings.Contains(id, "video") {
		return "Video model for image-to-video generation, editing, and extension workflows."
	}
	return "Image model for prompt-driven generation, editing, and visual design workflows."
}

func grokImagineCatalogContextWindow(id string) int64 {
	if strings.Contains(id, "video") {
		return 1024
	}
	return 64000
}

func grokImagineCatalogMaxOutput(id string) int64 {
	if strings.Contains(id, "video") {
		return 1024
	}
	return 16384
}

func inheritUpstreamModelVariantMetadata(metadata map[string]UpstreamModelMetadata, modelIDs []string) {
	if metadata == nil {
		return
	}
	for _, modelID := range modelIDs {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		baseID, suffix := upstreamModelVariantBaseID(modelID)
		if baseID == "" || strings.EqualFold(baseID, modelID) {
			continue
		}
		base, ok := metadata[baseID]
		if !ok || !upstreamModelMetadataIsUseful(base) {
			continue
		}
		current := metadata[modelID]
		fallback := base
		fallback.ID = modelID
		if strings.TrimSpace(fallback.DisplayName) != "" && !strings.EqualFold(strings.TrimSpace(fallback.DisplayName), baseID) {
			fallback.DisplayName = strings.TrimSpace(fallback.DisplayName) + " " + suffix
		} else {
			fallback.DisplayName = modelID
		}
		if desc := strings.TrimSpace(fallback.Description); desc != "" {
			note := upstreamModelVariantDescriptionNote(suffix)
			if note != "" && !strings.Contains(desc, note) {
				fallback.Description = desc + " " + note
			}
		}
		if strings.EqualFold(suffix, "Thinking") {
			if fallback.Reasoning == nil {
				reasoning := true
				fallback.Reasoning = &reasoning
			}
			if len(fallback.SupportedReasoningLevels) == 0 {
				if levels := claude.EffortLevelsForModel(baseID); len(levels) > 0 {
					fallback.SupportedReasoningLevels = append([]string(nil), levels...)
					if fallback.DefaultReasoningLevel == "" {
						fallback.DefaultReasoningLevel = levels[len(levels)-1]
					}
				}
			}
		}
		merged, _ := mergeUpstreamModelMetadata(current, fallback)
		if strings.TrimSpace(merged.ID) == "" {
			merged.ID = modelID
		}
		if strings.TrimSpace(merged.DisplayName) == "" {
			merged.DisplayName = modelID
		}
		metadata[modelID] = merged
	}
}

func upstreamModelVariantBaseID(modelID string) (string, string) {
	id := strings.TrimSpace(modelID)
	lower := strings.ToLower(id)
	switch {
	case strings.HasSuffix(lower, "-thinking"):
		return id[:len(id)-len("-thinking")], "Thinking"
	case strings.HasSuffix(lower, "-tiered"):
		return id[:len(id)-len("-tiered")], "Tiered"
	default:
		return "", ""
	}
}

func upstreamModelVariantDescriptionNote(suffix string) string {
	switch strings.ToLower(strings.TrimSpace(suffix)) {
	case "thinking":
		return "This ID is the extended-thinking variant of the same official model."
	case "tiered":
		return "This ID is the tiered routing variant of the same official model."
	default:
		return ""
	}
}

func filterUpstreamModelsByAccountPlatform(account *Account, models []string) []string {
	if account == nil || len(models) == 0 {
		return models
	}
	platform := strings.ToLower(strings.TrimSpace(upstreamModelSyncPlatform(account)))
	if platform == "" || platform == "openai" || platform == "anthropic" || platform == "gemini" {
		return models
	}
	filtered := make([]string, 0, len(models))
	for _, modelID := range models {
		if upstreamModelMatchesPlatform(platform, modelID) {
			filtered = append(filtered, modelID)
		}
	}
	return filtered
}

func upstreamModelMatchesPlatform(platform, modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return false
	}
	switch platform {
	case "deepseek":
		return strings.Contains(id, "deepseek")
	case "zhipu", "glm", "chatglm":
		return strings.Contains(id, "glm") || strings.Contains(id, "chatglm")
	case "kimi", "moonshot":
		return strings.Contains(id, "kimi") || strings.Contains(id, "moonshot")
	case "minimax":
		return strings.Contains(id, "minimax") || strings.Contains(id, "abab")
	case "grok", "xai":
		return strings.Contains(id, "grok")
	default:
		return true
	}
}

func lookupModelsDevModel(registry map[string]modelsDevProvider, account *Account, modelID string) (modelsDevModel, bool) {
	if preferred, ok := matchModelsDevProvider(registry, upstreamModelRegistryBaseURL(account)); ok {
		if model, found := findModelsDevModel(preferred, modelID); found {
			return model, true
		}
	}
	for _, provider := range registry {
		if model, found := findModelsDevModel(provider, modelID); found {
			return model, true
		}
	}
	return modelsDevModel{}, false
}

func findModelsDevModel(provider modelsDevProvider, modelID string) (modelsDevModel, bool) {
	if model, ok := provider.Models[modelID]; ok {
		return model, true
	}
	for candidateID, candidate := range provider.Models {
		if strings.EqualFold(strings.TrimSpace(candidateID), modelID) || strings.EqualFold(strings.TrimSpace(candidate.ID), modelID) {
			return candidate, true
		}
	}
	return modelsDevModel{}, false
}

func matchModelsDevProvider(registry map[string]modelsDevProvider, accountBaseURL string) (modelsDevProvider, bool) {
	if provider, ok := matchModelsDevProviderByAPIURL(registry, accountBaseURL); ok {
		return provider, true
	}
	return matchModelsDevProviderByKnownHost(registry, accountBaseURL)
}

func matchModelsDevProviderByAPIURL(registry map[string]modelsDevProvider, accountBaseURL string) (modelsDevProvider, bool) {
	accountBaseURL = normalizeModelRegistryBaseURL(accountBaseURL)
	if accountBaseURL == "" {
		return modelsDevProvider{}, false
	}
	var best modelsDevProvider
	bestScore := -1
	for _, provider := range registry {
		providerBaseURL := normalizeModelRegistryBaseURL(provider.API)
		if providerBaseURL == "" {
			continue
		}
		if accountBaseURL != providerBaseURL &&
			!strings.HasPrefix(accountBaseURL, providerBaseURL+"/") &&
			!strings.HasPrefix(providerBaseURL, accountBaseURL+"/") {
			continue
		}
		if len(providerBaseURL) > bestScore {
			best = provider
			bestScore = len(providerBaseURL)
		}
	}
	return best, bestScore >= 0
}

// matchModelsDevProviderByKnownHost covers first-party hosts whose models.dev
// entries omit the `api` field (notably the official OpenAI provider). Custom
// compatible gateways must still match by API URL so same-named models are not
// cross-attributed across vendors.
func matchModelsDevProviderByKnownHost(registry map[string]modelsDevProvider, accountBaseURL string) (modelsDevProvider, bool) {
	host := modelRegistryHostname(accountBaseURL)
	if host == "" {
		return modelsDevProvider{}, false
	}
	providerID := ""
	switch host {
	case "api.openai.com", "chatgpt.com":
		providerID = "openai"
	default:
		return modelsDevProvider{}, false
	}
	provider, ok := registry[providerID]
	if !ok || len(provider.Models) == 0 {
		return modelsDevProvider{}, false
	}
	if strings.TrimSpace(provider.ID) == "" {
		provider.ID = providerID
	}
	return provider, true
}

func modelRegistryHostname(raw string) string {
	normalized := normalizeModelRegistryBaseURL(raw)
	if normalized == "" {
		return ""
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func normalizeModelRegistryBaseURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	path := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(strings.ToLower(path), "/models") {
		path = strings.TrimRight(path[:len(path)-len("/models")], "/")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path
}

func (s *AccountTestService) fetchUpstreamModelList(ctx context.Context, account *Account) ([]string, []byte, error) {
	if s == nil {
		return nil, nil, newUpstreamModelSyncConfigError("Account test service is not configured", nil)
	}
	if account == nil {
		return nil, nil, newUpstreamModelSyncConfigError("Account is required", nil)
	}

	if account.Platform == PlatformAntigravity && account.Type != AccountTypeAPIKey {
		models, err := s.fetchAntigravityOAuthUpstreamModels(ctx, account)
		return models, nil, err
	}

	if s.httpUpstream == nil {
		return nil, nil, newUpstreamModelSyncConfigError("Upstream HTTP client is not configured", nil)
	}

	req, err := s.buildUpstreamModelsRequest(ctx, account)
	if err != nil {
		return nil, nil, err
	}

	proxyURL := upstreamModelsProxyURL(account)
	resp, err := s.doUpstreamModelsRequest(req, proxyURL, account)
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Failed to request upstream model list", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyLimit := resolveModelsListReadLimit(s.cfg)
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Failed to read upstream model list", err)
	}
	if int64(len(body)) > bodyLimit {
		return nil, nil, newUpstreamModelSyncUpstreamError("Upstream model list response is too large", fmt.Errorf("response exceeds %d bytes", bodyLimit))
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, &UpstreamModelSyncError{
			Kind:       UpstreamModelSyncErrorUpstream,
			Message:    fmt.Sprintf("Upstream model list request failed with HTTP %d", resp.StatusCode),
			StatusCode: resp.StatusCode,
			Err:        fmt.Errorf("upstream model list returned HTTP %d", resp.StatusCode),
		}
	}

	extractModels := extractUpstreamModelIDs
	if account.IsGrok() {
		extractModels = extractGrokUpstreamModelIDs
	}
	models, err := extractModels(body)
	if err != nil {
		return nil, nil, newUpstreamModelSyncUpstreamError("Upstream model list response was not valid JSON", err)
	}
	if len(models) == 0 {
		return nil, nil, newUpstreamModelSyncUpstreamError("Upstream returned no supported models", nil)
	}

	return models, body, nil
}

func (s *AccountTestService) buildUpstreamModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	switch {
	case account.Platform == PlatformAntigravity:
		return s.buildAntigravityAPIKeyModelsRequest(ctx, account)
	case account.IsGrok():
		return s.buildGrokUpstreamModelsRequest(ctx, account)
	case account.IsOpenAI() || account.IsCNProvider() || account.IsGeminiOpenAIProtocol():
		// 国产 OpenAI 兼容供应商和 Gemini 自定义协议账号复用 OpenAI /v1/models 探测。
		return s.buildOpenAIUpstreamModelsRequest(ctx, account)
	case account.IsGemini():
		return s.buildGeminiUpstreamModelsRequest(ctx, account)
	case account.IsAnthropic():
		return s.buildAnthropicUpstreamModelsRequest(ctx, account)
	default:
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported platform for upstream model sync: %s", account.Platform), nil,
		)
	}
}

func (s *AccountTestService) buildGrokUpstreamModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	if account == nil {
		return nil, newUpstreamModelSyncConfigError("Account is required", nil)
	}

	var (
		authToken         string
		normalizedBaseURL string
		isOAuth           = account.IsGrokOAuth()
	)
	switch account.Type {
	case AccountTypeAPIKey:
		authToken = strings.TrimSpace(account.GetCredential("api_key"))
		if authToken == "" {
			return nil, newUpstreamModelSyncConfigError("No Grok API key is available", nil)
		}

		baseURL := strings.TrimSpace(account.GetCredential("base_url"))
		if baseURL == "" {
			baseURL = "https://api.x.ai"
		}
		validatedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Grok base URL", err)
		}
		normalizedBaseURL = validatedBaseURL
	case AccountTypeOAuth:
		if s.grokTokenProvider == nil {
			return nil, newUpstreamModelSyncConfigError("Grok token provider is not configured", nil)
		}
		accessToken, err := s.grokTokenProvider.GetAccessTokenForManualTest(ctx, account)
		if err != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to get Grok access token", err)
		}
		authToken = strings.TrimSpace(accessToken)
		if authToken == "" {
			return nil, newUpstreamModelSyncConfigError("No Grok access token is available", nil)
		}

		validator, err := grokBaseURLValidator(account, s.cfg)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Grok base URL", err)
		}
		baseURL := account.GetGrokBaseURL()
		if s.settingService != nil {
			baseURL = s.settingService.ResolveGrokBaseURL(ctx, account)
		}
		validatedBaseURL, err := validator(baseURL)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Grok base URL", err)
		}
		normalizedBaseURL = validatedBaseURL
	default:
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported Grok account type for upstream model sync: %s", account.Type), nil,
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildOpenAIModelsURL(normalizedBaseURL), nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Grok model list URL", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken)
	if isOAuth {
		// The shared HTTP transport adds the official CLI marker/version for the
		// exact proxy host. Keep the request builder aligned with the other Grok
		// probes and only forward account identity headers to that trusted host.
		applyGrokCLIHeaders(req.Header)
		if isGrokCLIProxyTarget(req.URL.String()) {
			if userID := strings.TrimSpace(account.GetCredential("sub")); userID != "" {
				req.Header.Set("X-UserID", userID)
			}
			if email := strings.TrimSpace(account.GetCredential("email")); email != "" {
				req.Header.Set("X-Email", email)
			}
		}
	}
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

func (s *AccountTestService) buildAnthropicUpstreamModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	if account.IsBedrock() || account.Type == AccountTypeServiceAccount {
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported Anthropic account type for upstream model sync: %s", account.Type), nil,
		)
	}

	baseURL := "https://api.anthropic.com"
	authHeaderName := ""
	authHeaderValue := ""
	apiKeyAuthToken := ""
	betaHeader := ""

	if account.IsOAuth() {
		accessToken := strings.TrimSpace(account.GetCredential("access_token"))
		if accessToken == "" && s.claudeTokenProvider != nil {
			token, tokenErr := s.claudeTokenProvider.GetAccessToken(ctx, account)
			if tokenErr != nil {
				return nil, newUpstreamModelSyncUpstreamError("Failed to get Anthropic access token", tokenErr)
			}
			accessToken = strings.TrimSpace(token)
		}
		if accessToken == "" {
			return nil, newUpstreamModelSyncConfigError("No Anthropic access token is available", nil)
		}
		authHeaderName = "Authorization"
		authHeaderValue = "Bearer " + accessToken
		betaHeader = claude.DefaultBetaHeader
	} else if account.Type == AccountTypeAPIKey {
		apiKey := strings.TrimSpace(account.GetCredential("api_key"))
		if apiKey == "" {
			return nil, newUpstreamModelSyncConfigError("No Anthropic API key is available", nil)
		}
		baseURL = account.GetBaseURL()
		if strings.TrimSpace(baseURL) == "" {
			baseURL = "https://api.anthropic.com"
		}
		apiKeyAuthToken = apiKey
		betaHeader = claude.APIKeyBetaHeader
	} else {
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported Anthropic account type for upstream model sync: %s", account.Type), nil,
		)
	}

	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Anthropic base URL", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildV1ModelsURL(normalizedBaseURL), nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Anthropic model list URL", err)
	}
	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", betaHeader)
	if authHeaderName != "" {
		req.Header.Set(authHeaderName, authHeaderValue)
	} else {
		// Ollama Cloud Anthropic 兼容端点按实际 base_url 强制 Bearer，其余保持
		// extra/default 行为。
		setAnthropicAPIKeyAuthHeader(req.Header, account, apiKeyAuthToken, normalizedBaseURL)
	}
	// 账号级请求头覆写：模型列表探测与真实转发保持一致的最终头
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

func (s *AccountTestService) buildAntigravityAPIKeyModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	if account.Type != AccountTypeAPIKey {
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported Antigravity account type for upstream model sync: %s", account.Type), nil,
		)
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	if apiKey == "" {
		return nil, newUpstreamModelSyncConfigError("No Antigravity API key is available", nil)
	}

	baseURL := strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/")
	if baseURL == "" {
		return nil, newUpstreamModelSyncConfigError("Antigravity API-key base URL is required for upstream model sync", nil)
	}
	if !strings.HasSuffix(strings.ToLower(baseURL), "/antigravity") {
		return nil, newUpstreamModelSyncUnsupportedError(
			"Antigravity API-key upstream model sync requires a compatible gateway base URL ending in /antigravity; use Antigravity OAuth for official Cloud Code upstreams",
			nil,
		)
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Antigravity base URL", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildV1ModelsURL(normalizedBaseURL), nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Antigravity model list URL", err)
	}
	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
	req.Header.Set("x-api-key", apiKey)
	return req, nil
}

func (s *AccountTestService) buildOpenAIUpstreamModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	if account.IsOpenAIOAuth() {
		return s.buildOpenAIOAuthUpstreamModelsRequest(ctx, account)
	}
	return buildOpenAIAPIKeyModelsRequest(ctx, account, s.validateUpstreamBaseURL)
}

// buildOpenAIAPIKeyModelsRequest is shared by admin discovery and public model
// listing. Codex content negotiation is intentionally absent from this request.
func buildOpenAIAPIKeyModelsRequest(ctx context.Context, account *Account, validateBaseURL func(string) (string, error)) (*http.Request, error) {
	if account.Type != AccountTypeAPIKey {
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported OpenAI account type for upstream model sync: %s", account.Type), nil,
		)
	}
	apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return nil, newUpstreamModelSyncConfigError("No OpenAI API key is available", nil)
	}

	// 协议感知：Anthropic 协议账号的凭证 base_url 指向 /anthropic 端点，模型
	// 列表同步需使用 OpenAI 格式 base（供应商 × 模式默认）。
	baseURL := account.GetOpenAIFormatBaseURL()
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com"
	}
	normalizedBaseURL, err := validateBaseURL(baseURL)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid OpenAI base URL", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildOpenAIModelsURL(normalizedBaseURL), nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid OpenAI model list URL", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	// 账号级请求头覆写：模型列表探测与真实转发保持一致的最终头
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

// buildOpenAIOAuthUpstreamModelsRequest uses ChatGPT's Codex model manifest.
// OAuth subscriptions do not expose the public Platform API /v1/models endpoint,
// so treating them like API-key accounts makes the admin sync button fail locally.
func (s *AccountTestService) buildOpenAIOAuthUpstreamModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	credentialAccount, err := resolveCredentialAccount(ctx, s.accountRepo, account)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Failed to resolve OpenAI account credentials", err)
	}
	if !credentialAccount.IsOpenAIOAuth() {
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported OpenAI account type for upstream model sync: %s", credentialAccount.Type), nil,
		)
	}

	modelsURL, err := buildCodexModelsManifestURL(
		chatgptCodexModelsURL,
		false,
		CodexCanonicalClientVersion(),
	)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid OpenAI Codex model list URL", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL.String(), nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid OpenAI Codex model list request", err)
	}

	if credentialAccount.IsOpenAIAgentIdentity() {
		authHeaders, authErr := buildAgentIdentityAuthenticationHeaders(
			ctx,
			s.accountRepo,
			s.agentIdentityWS,
			&s.agentIdentityTaskMu,
			credentialAccount,
		)
		if authErr != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to build OpenAI Agent Identity authentication", authErr)
		}
		for key, values := range authHeaders {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	} else {
		accessToken := strings.TrimSpace(credentialAccount.GetOpenAIAccessToken())
		if accessToken == "" {
			return nil, newUpstreamModelSyncConfigError("No OpenAI access token is available", nil)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	identity := resolveCodexOutboundIdentity(credentialAccount.GetOpenAIUserAgent())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Originator", identity.originator)
	req.Header.Set("User-Agent", identity.userAgent)
	req.Header.Set("Version", identity.version)
	setOpenAIChatGPTAccountHeaders(req.Header, credentialAccount)
	credentialAccount.ApplyHeaderOverrides(req.Header)
	enforceCodexIdentityHeadersWithUA(req.Header, credentialAccount.GetOpenAIUserAgent())
	return req, nil
}

func (s *AccountTestService) buildGeminiUpstreamModelsRequest(ctx context.Context, account *Account) (*http.Request, error) {
	baseURL := account.GetGeminiBaseURL(geminicli.AIStudioBaseURL)
	if strings.TrimSpace(baseURL) == "" {
		baseURL = geminicli.AIStudioBaseURL
	}
	normalizedBaseURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Gemini base URL", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, buildGeminiModelsURL(normalizedBaseURL), nil)
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Gemini model list URL", err)
	}
	req.Header.Set("Accept", "application/json")

	switch account.Type {
	case AccountTypeAPIKey:
		apiKey := strings.TrimSpace(account.GetCredential("api_key"))
		if apiKey == "" {
			return nil, newUpstreamModelSyncConfigError("No Gemini API key is available", nil)
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case AccountTypeOAuth:
		if strings.TrimSpace(account.GetCredential("project_id")) != "" {
			return nil, newUpstreamModelSyncUnsupportedError("Gemini Code Assist model listing is not supported by this sync button", nil)
		}
		if s.geminiTokenProvider == nil {
			return nil, newUpstreamModelSyncConfigError("Gemini token provider is not configured", nil)
		}
		accessToken, tokenErr := s.geminiTokenProvider.GetAccessToken(ctx, account)
		if tokenErr != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to get Gemini access token", tokenErr)
		}
		accessToken = strings.TrimSpace(accessToken)
		if accessToken == "" {
			return nil, newUpstreamModelSyncConfigError("No Gemini access token is available", nil)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
	default:
		return nil, newUpstreamModelSyncUnsupportedError(
			fmt.Sprintf("Unsupported Gemini account type for upstream model sync: %s", account.Type), nil,
		)
	}

	return req, nil
}

func (s *AccountTestService) fetchAntigravityOAuthUpstreamModels(ctx context.Context, account *Account) ([]string, error) {
	if s.antigravityGatewayService == nil || s.antigravityGatewayService.GetTokenProvider() == nil {
		return nil, newUpstreamModelSyncConfigError("Antigravity token provider is not configured", nil)
	}

	accessToken, err := s.antigravityGatewayService.GetTokenProvider().GetAccessToken(ctx, account)
	if err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Failed to get Antigravity access token", err)
	}
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return nil, newUpstreamModelSyncConfigError("No Antigravity access token is available", nil)
	}

	client, err := antigravity.NewClient(upstreamModelsProxyURL(account))
	if err != nil {
		return nil, newUpstreamModelSyncConfigError("Failed to configure Antigravity client", err)
	}
	modelsResp, _, err := client.FetchAvailableModels(
		ctx,
		accessToken,
		strings.TrimSpace(account.GetCredential("project_id")),
		resolveModelsListReadLimit(s.cfg),
	)
	if err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Failed to fetch Antigravity available models", err)
	}
	if modelsResp == nil || len(modelsResp.Models) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Upstream returned no supported models", nil)
	}

	models := make([]string, 0, len(modelsResp.Models))
	for modelID := range modelsResp.Models {
		models = append(models, strings.TrimSpace(modelID))
	}
	return dedupeAndSortModelIDs(models), nil
}

func (s *AccountTestService) doUpstreamModelsRequest(req *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.tlsFPProfileService == nil {
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, nil)
	}
	return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
}

func upstreamModelsProxyURL(account *Account) string {
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		return account.Proxy.URL()
	}
	return ""
}

func buildV1ModelsURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(normalized, "/v1/models") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + "/models"
	}
	return normalized + "/v1/models"
}

func buildOpenAIModelsURL(base string) string {
	return buildOpenAIEndpointURL(base, "/v1/models")
}

func buildGeminiModelsURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(normalized, "/v1beta/models") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1beta") {
		return normalized + "/models"
	}
	return normalized + "/v1beta/models"
}

type upstreamModelEntry struct {
	ID           string          `json:"id"`
	Slug         string          `json:"slug"`
	Model        string          `json:"model"`
	ModelID      string          `json:"modelId"`
	ModelIDSnake string          `json:"model_id"`
	Name         string          `json:"name"`
	Meta         json.RawMessage `json:"_meta"`
}

type upstreamModelEntryMetadata struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Model        string `json:"model"`
	ModelID      string `json:"modelId"`
	ModelIDSnake string `json:"model_id"`
	Name         string `json:"name"`
}

type upstreamModelCapabilityEntry struct {
	upstreamModelEntry
	DisplayName              string                     `json:"display_name"`
	Description              string                     `json:"description"`
	Reasoning                *bool                      `json:"reasoning"`
	DefaultReasoningLevel    string                     `json:"default_reasoning_level"`
	SupportedReasoningLevels []json.RawMessage          `json:"supported_reasoning_levels"`
	ReasoningOptions         []modelsDevReasoningOption `json:"reasoning_options"`
	InputModalities          []string                   `json:"input_modalities"`
	Modalities               modelsDevModalities        `json:"modalities"`
	ContextWindow            int64                      `json:"context_window"`
	MaxContextWindow         int64                      `json:"max_context_window"`
	MaxOutputTokens          int64                      `json:"max_output_tokens"`
	Limit                    modelsDevLimit             `json:"limit"`
}

func extractUpstreamModelIDs(body []byte) ([]string, error) {
	return extractUpstreamModelIDsWithSelector(body, upstreamModelEntryID)
}

func extractGrokUpstreamModelIDs(body []byte) ([]string, error) {
	return extractUpstreamModelIDsWithSelector(body, grokUpstreamModelEntryID)
}

func extractUpstreamModelCatalog(body []byte, grok bool) ([]string, map[string]UpstreamModelMetadata, error) {
	entries, err := extractUpstreamModelRawEntries(body)
	if err != nil {
		return nil, nil, err
	}
	selectID := upstreamModelEntryID
	if grok {
		selectID = grokUpstreamModelEntryID
	}

	models := make([]string, 0, len(entries))
	metadata := make(map[string]UpstreamModelMetadata)
	for _, raw := range entries {
		var capability upstreamModelCapabilityEntry
		if err := json.Unmarshal(raw, &capability); err != nil {
			continue
		}
		modelID := strings.TrimSpace(selectID(capability.upstreamModelEntry))
		if modelID == "" {
			continue
		}
		models = append(models, modelID)
		entry := upstreamMetadataFromCapabilityEntry(modelID, capability)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			entry.CodexToolCapabilities = make(map[string]json.RawMessage)
			applyCodexToolCapabilities(entry.CodexToolCapabilities, fields, true)
		}
		if upstreamModelMetadataIsUseful(entry) {
			metadata[modelID] = entry
		}
	}
	return dedupeAndSortModelIDs(models), metadata, nil
}

func extractUpstreamModelRawEntries(body []byte) ([]json.RawMessage, error) {
	var response struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err == nil && (response.Data != nil || response.Models != nil) {
		entries := make([]json.RawMessage, 0, len(response.Data)+len(response.Models))
		entries = append(entries, response.Data...)
		entries = append(entries, response.Models...)
		return entries, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parse upstream model catalog: %w", err)
	}
	return entries, nil
}

func upstreamMetadataFromCapabilityEntry(modelID string, entry upstreamModelCapabilityEntry) UpstreamModelMetadata {
	levels := reasoningLevelsFromRawEntries(entry.SupportedReasoningLevels)
	if len(levels) == 0 {
		levels = reasoningLevelsFromModelsDevOptions(entry.ReasoningOptions)
	}
	reasoning := entry.Reasoning
	if reasoning == nil && len(levels) > 0 {
		inferred := len(levels) != 1 || levels[0] != "none"
		reasoning = &inferred
	}
	modalities := entry.InputModalities
	if len(modalities) == 0 {
		modalities = entry.Modalities.Input
	}
	contextWindow := entry.ContextWindow
	if contextWindow <= 0 {
		contextWindow = entry.MaxContextWindow
	}
	if contextWindow <= 0 {
		contextWindow = entry.Limit.Context
	}
	maxOutputTokens := entry.MaxOutputTokens
	if maxOutputTokens <= 0 {
		maxOutputTokens = entry.Limit.Output
	}
	defaultReasoningLevel := normalizeReasoningLevel(entry.DefaultReasoningLevel)
	if defaultReasoningLevel == "" && len(levels) > 0 {
		defaultReasoningLevel = levels[0]
	}
	displayName := strings.TrimSpace(entry.DisplayName)
	if displayName == "" && strings.TrimSpace(entry.Name) != "" && strings.TrimSpace(entry.Name) != modelID {
		displayName = strings.TrimSpace(entry.Name)
	}
	return UpstreamModelMetadata{
		ID:                       modelID,
		DisplayName:              displayName,
		Description:              strings.TrimSpace(entry.Description),
		Reasoning:                reasoning,
		DefaultReasoningLevel:    defaultReasoningLevel,
		SupportedReasoningLevels: levels,
		InputModalities:          normalizeCodexInputModalities(modalities),
		ContextWindow:            contextWindow,
		MaxOutputTokens:          maxOutputTokens,
	}
}

func reasoningLevelsFromRawEntries(entries []json.RawMessage) []string {
	levels := make([]string, 0, len(entries))
	for _, raw := range entries {
		var effort string
		if err := json.Unmarshal(raw, &effort); err == nil {
			levels = append(levels, effort)
			continue
		}
		var level struct {
			Effort string `json:"effort"`
		}
		if err := json.Unmarshal(raw, &level); err == nil {
			levels = append(levels, level.Effort)
		}
	}
	return normalizeReasoningLevels(levels)
}

func normalizeReasoningLevels(levels []string) []string {
	seen := make(map[string]struct{}, len(levels))
	normalized := make([]string, 0, len(levels))
	for _, level := range levels {
		level = normalizeReasoningLevel(level)
		if level == "" {
			continue
		}
		if _, exists := seen[level]; exists {
			continue
		}
		seen[level] = struct{}{}
		normalized = append(normalized, level)
	}
	return normalized
}

func normalizeReasoningLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "off", "disabled":
		return "none"
	case "extra-high", "extra_high":
		return "xhigh"
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return level
	default:
		return ""
	}
}

func normalizeCodexInputModalities(modalities []string) []string {
	seen := make(map[string]struct{}, len(modalities))
	normalized := make([]string, 0, len(modalities))
	for _, modality := range modalities {
		modality = strings.ToLower(strings.TrimSpace(modality))
		if modality != "text" && modality != "image" {
			continue
		}
		if _, exists := seen[modality]; exists {
			continue
		}
		seen[modality] = struct{}{}
		normalized = append(normalized, modality)
	}
	return normalized
}

func extractUpstreamModelIDsWithSelector(body []byte, selectID func(upstreamModelEntry) string) ([]string, error) {
	var response struct {
		Data   []upstreamModelEntry `json:"data"`
		Models []upstreamModelEntry `json:"models"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		var arrayResponse []upstreamModelEntry
		if arrayErr := json.Unmarshal(body, &arrayResponse); arrayErr != nil {
			return nil, fmt.Errorf("parse upstream model list: %w", err)
		}

		models := make([]string, 0, len(arrayResponse))
		for _, entry := range arrayResponse {
			models = append(models, selectID(entry))
		}
		return dedupeAndSortModelIDs(models), nil
	}

	models := make([]string, 0, len(response.Data)+len(response.Models))
	for _, entry := range response.Data {
		models = append(models, selectID(entry))
	}
	for _, entry := range response.Models {
		models = append(models, selectID(entry))
	}

	if len(models) == 0 {
		var arrayResponse []upstreamModelEntry
		if err := json.Unmarshal(body, &arrayResponse); err == nil {
			for _, entry := range arrayResponse {
				models = append(models, selectID(entry))
			}
		}
	}

	return dedupeAndSortModelIDs(models), nil
}

func upstreamModelEntryID(entry upstreamModelEntry) string {
	modelID := strings.TrimSpace(entry.ID)
	if modelID == "" {
		modelID = strings.TrimSpace(entry.Slug)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(entry.Name)
	}
	return strings.TrimPrefix(modelID, "models/")
}

func grokUpstreamModelEntryID(entry upstreamModelEntry) string {
	candidates := []string{
		entry.Model,
		entry.ModelID,
		entry.ModelIDSnake,
		entry.ID,
		entry.Slug,
	}
	if len(entry.Meta) > 0 {
		var meta upstreamModelEntryMetadata
		if err := json.Unmarshal(entry.Meta, &meta); err == nil {
			candidates = append(candidates,
				meta.Model,
				meta.ModelID,
				meta.ModelIDSnake,
				meta.ID,
				meta.Slug,
				meta.Name,
			)
		}
	}
	// `name` is a display label in the Grok catalog, so keep it as the final
	// compatibility fallback rather than preferring it over protocol model IDs.
	candidates = append(candidates, entry.Name)
	for _, candidate := range candidates {
		modelID := strings.TrimSpace(candidate)
		if modelID != "" {
			return strings.TrimPrefix(modelID, "models/")
		}
	}
	return ""
}

func dedupeAndSortModelIDs(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	result := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}
