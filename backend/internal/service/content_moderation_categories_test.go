package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTypeSafeCategoriesPreserveDefaultsAndExplicitDisabled(t *testing.T) {
	settings := mergeModerationCategorySettings(ContentModerationEngineTypeSafe, nil, nil)
	require.Len(t, settings, 15)
	require.True(t, settings["sexual"].Enabled)
	require.False(t, settings["jailbreak"].Enabled)
	require.False(t, settings["control_bypass"].Enabled)
	settings["sexual"] = ContentModerationCategorySetting{Enabled: false}
	settings["jailbreak"] = ContentModerationCategorySetting{Enabled: true}
	kept := mergeModerationCategorySettings(ContentModerationEngineTypeSafe, settings, nil)
	require.False(t, kept["sexual"].Enabled, "older clients omitting switches must not reset them")
	require.True(t, kept["jailbreak"].Enabled)
	cfg := &ContentModerationConfig{Engine: ContentModerationEngineTypeSafe, CategorySettings: kept}
	questions := typeSafeModerationQuestions(cfg)
	require.NotContains(t, questions, "sexual", "disabled checks must not be sent upstream")
	require.Contains(t, questions, "jailbreak")
	require.NotContains(t, questions, "control_bypass")
}

func TestTypeSafeDisabledHighScoreCannotBlockAndSnapshotIsIndependent(t *testing.T) {
	cfg := &ContentModerationConfig{Engine: ContentModerationEngineTypeSafe, Thresholds: ContentModerationDefaultThresholds(ContentModerationEngineTypeSafe), CategorySettings: map[string]ContentModerationCategorySetting{"sexual": {Enabled: false}}}
	result := &moderationAPIResult{CategoryScores: map[string]float64{"sexual": 1, "jailbreak": 1, "violence": 0.1}, EngineMeta: &ContentModerationEngineMeta{Engine: cfg.Engine}}
	flagged, highest, _ := evaluateModerationScoresForConfig(result.CategoryScores, cfg)
	require.False(t, flagged)
	require.Equal(t, "violence", highest)
	meta := moderationResultMeta(cfg, result)
	require.Empty(t, meta.MatchedCategories)
	cfg.CategorySettings["sexual"] = ContentModerationCategorySetting{Enabled: true}
	flagged, _, _ = evaluateModerationScoresForConfig(result.CategoryScores, cfg)
	require.True(t, flagged)
	require.False(t, meta.CategorySettings["sexual"].Enabled, "stored snapshots must not change with live settings")
}

func TestTypeSafeHashDecisionChangesWithSwitchesAndThresholds(t *testing.T) {
	cfg := &ContentModerationConfig{Engine: ContentModerationEngineTypeSafe, Thresholds: ContentModerationDefaultThresholds(ContentModerationEngineTypeSafe)}
	before := moderationDecisionHash("synthetic-content-digest", cfg)
	cfg.CategorySettings = map[string]ContentModerationCategorySetting{"sexual": {Enabled: false}}
	after := moderationDecisionHash("synthetic-content-digest", cfg)
	require.NotEqual(t, before, after, "old hash blocks must not survive disabling their category")
	cfg.Thresholds["hate"] = 0.99
	require.NotEqual(t, after, moderationDecisionHash("synthetic-content-digest", cfg))
	thresholds := map[string]float64{"sexual": math.NaN()}
	require.Error(t, validateModerationThresholds(ContentModerationEngineTypeSafe, &thresholds))
}
