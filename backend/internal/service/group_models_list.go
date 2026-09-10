package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
)

// GroupModelsListConfig is the kedaya-era name for the group model allowlist.
type GroupModelsListConfig = GroupModelAllowlist

func normalizeGroupModelsListConfig(cfg GroupModelsListConfig) GroupModelsListConfig {
	out := GroupModelsListConfig{Enabled: cfg.Enabled}
	if len(cfg.Models) == 0 {
		return out
	}

	seen := make(map[string]struct{}, len(cfg.Models))
	out.Models = make([]string, 0, len(cfg.Models))
	for _, model := range cfg.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Models = append(out.Models, model)
	}
	if len(out.Models) == 0 {
		out.Models = nil
	}
	return out
}

func (g *Group) CustomModelsListEnabled() bool {
	return g != nil && g.ModelAllowlist.Enabled && len(g.ModelAllowlist.Models) > 0
}

// modelsListAllows reports whether a client-requested model hits this group list.
// Matching uses the same candidate forms as the official group allowlist:
// Gemini models/ prefix, Claude -thinking tolerance, and OpenAI reasoning suffixes.
func ModelsListAllows(a GroupModelsListConfig, model string) bool {
	if !a.Enabled {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return true
	}
	candidates := groupModelListCandidates(model)
	for _, entry := range a.Models {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		entry = strings.ToLower(entry)
		if strings.HasSuffix(entry, "*") {
			prefix := strings.TrimSuffix(entry, "*")
			for _, candidate := range candidates {
				if strings.HasPrefix(candidate, prefix) {
					return true
				}
			}
			continue
		}
		for _, candidate := range candidates {
			if candidate == entry {
				return true
			}
		}
	}
	return false
}

func groupModelListCandidates(model string) []string {
	candidates := make([]string, 0, 4)
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
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

	add(model)
	add(strings.TrimPrefix(model, "models/"))
	add(claude.NormalizeModelID(strings.TrimSuffix(model, "-thinking")))
	add(NormalizeOpenAICompatRequestedModel(model))
	return candidates
}

// filterModelsListForListing emits models in list-entry order. Exact entries
// must exist in source; trailing wildcards expand to matching source items.
func FilterModelsListForListing(a GroupModelsListConfig, source []string) []string {
	if !a.Enabled {
		return source
	}
	if len(a.Models) == 0 {
		return nil
	}

	patterns := make([]string, 0, len(source))
	for _, pattern := range source {
		pattern = strings.TrimSpace(pattern)
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	if len(patterns) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(a.Models)+len(patterns))
	filtered := make([]string, 0, len(patterns))
	add := func(model string) {
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		filtered = append(filtered, model)
	}

	for _, entry := range a.Models {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.HasSuffix(entry, "*") {
			prefix := strings.ToLower(strings.TrimSuffix(entry, "*"))
			for _, pattern := range patterns {
				if strings.HasPrefix(strings.ToLower(pattern), prefix) {
					add(pattern)
				}
			}
			continue
		}
		if groupListSourceAllowsModel(patterns, entry) {
			add(entry)
		}
	}
	return filtered
}

func groupListSourceAllowsModel(patterns []string, model string) bool {
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, model) {
			return true
		}
		if strings.HasSuffix(pattern, "*") && strings.HasPrefix(strings.ToLower(model), strings.ToLower(strings.TrimSuffix(pattern, "*"))) {
			return true
		}
	}
	normalizedClaudeModel := claude.NormalizeModelID(strings.TrimSuffix(model, "-thinking"))
	if !strings.EqualFold(normalizedClaudeModel, model) {
		for _, pattern := range patterns {
			if strings.EqualFold(pattern, normalizedClaudeModel) {
				return true
			}
		}
	}
	return false
}
