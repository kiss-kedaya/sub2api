package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type ContentModerationCategorySetting struct {
	Enabled bool `json:"enabled"`
}

// Preserve the existing thirteen enabled checks. Newly added categories are
// opt-in; omitted fields from an older client never reset a saved switch.
func mergeModerationCategorySettings(engine string, base, override map[string]ContentModerationCategorySetting) map[string]ContentModerationCategorySetting {
	out := map[string]ContentModerationCategorySetting{}
	if engine != ContentModerationEngineTypeSafe {
		return out
	}
	for _, category := range ContentModerationCategories(engine) {
		setting := ContentModerationCategorySetting{Enabled: category != "jailbreak" && category != "control_bypass"}
		if saved, ok := base[category]; ok {
			setting = saved
		}
		if supplied, ok := override[category]; ok {
			setting = supplied
		}
		out[category] = setting
	}
	return out
}

func cloneModerationCategorySettings(settings map[string]ContentModerationCategorySetting) map[string]ContentModerationCategorySetting {
	if settings == nil {
		return nil
	}
	out := make(map[string]ContentModerationCategorySetting, len(settings))
	for key, value := range settings {
		out[key] = value
	}
	return out
}

func moderationCategoryEnabled(engine string, settings map[string]ContentModerationCategorySetting, category string) bool {
	if category == "jailbreak" || category == "control_bypass" {
		return engine == ContentModerationEngineTypeSafe && settings[category].Enabled
	}
	if engine == ContentModerationEngineTypeSafe {
		if supplied, ok := settings[category]; ok {
			return supplied.Enabled
		}
	}
	return true
}

func validateModerationThresholds(engine string, thresholds *map[string]float64) error {
	if thresholds != nil {
		for _, category := range ContentModerationCategories(engine) {
			if value, ok := (*thresholds)[category]; ok && (math.IsNaN(value) || math.IsInf(value, 0)) {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_THRESHOLD", "审计阈值必须为有限数值")
			}
		}
	}
	return nil
}

func evaluateModerationScoresForConfig(scores map[string]float64, cfg *ContentModerationConfig) (bool, string, float64) {
	flagged, highest, score := false, "", 0.0
	for _, category := range ContentModerationCategories(cfg.Engine) {
		value, present := scores[category]
		if !present || !moderationCategoryEnabled(cfg.Engine, cfg.CategorySettings, category) {
			continue
		}
		if highest == "" || value > score {
			highest, score = category, value
		}
		if value >= cfg.Thresholds[category] {
			flagged = true
		}
	}
	return flagged, highest, score
}

func moderationResultMeta(cfg *ContentModerationConfig, result *moderationAPIResult) *ContentModerationEngineMeta {
	if result.EngineMeta == nil {
		return nil
	}
	meta := *result.EngineMeta
	meta.CategorySettings = mergeModerationCategorySettings(cfg.Engine, nil, cfg.CategorySettings)
	meta.MatchedCategories = []string{}
	for _, category := range ContentModerationCategories(cfg.Engine) {
		if score, ok := result.CategoryScores[category]; ok && moderationCategoryEnabled(cfg.Engine, cfg.CategorySettings, category) && score >= cfg.Thresholds[category] {
			meta.MatchedCategories = append(meta.MatchedCategories, category)
		}
	}
	return &meta
}

// TypeSafe hash shortcuts must not replay an older category/threshold decision
// after the operator disables that category. The hash contains no prompt data.
func moderationDecisionHash(contentHash string, cfg *ContentModerationConfig) string {
	if cfg.Engine != ContentModerationEngineTypeSafe {
		return contentHash
	}
	raw, _ := json.Marshal(struct {
		Version    string                                      `json:"version"`
		Thresholds map[string]float64                          `json:"thresholds"`
		Settings   map[string]ContentModerationCategorySetting `json:"settings"`
	}{TypeSafeModerationRulesVersion, cfg.Thresholds, mergeModerationCategorySettings(cfg.Engine, nil, cfg.CategorySettings)})
	sum := sha256.Sum256(append([]byte(contentHash+"\n"), raw...))
	return hex.EncodeToString(sum[:])
}
