package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsDefaultScheduledTestPrompt(t *testing.T) {
	require.True(t, isDefaultScheduledTestPrompt(""))
	require.True(t, isDefaultScheduledTestPrompt("   "))
	require.True(t, isDefaultScheduledTestPrompt(DefaultScheduledTestPrompt))
	for _, legacy := range legacyScheduledTestPrompts {
		require.NotEmpty(t, legacy, "legacy 列表里不应有空题面")
		require.NotEqual(t, DefaultScheduledTestPrompt, legacy, "legacy 列表不应包含当前默认值")
		require.True(t, isDefaultScheduledTestPrompt(legacy), "历史默认题面必须按默认处理")
	}
	require.False(t, isDefaultScheduledTestPrompt("帮我写一首关于秋天的诗"))
}

// 旧计划存的题面是历届默认值：评估器必须照常运行，而不是报 custom prompt。
func TestLegacyPromptStillRunsQualityEvaluator(t *testing.T) {
	content := `<html><svg width="10" height="10" xmlns="http://www.w3.org/2000/svg"><circle r="4"><animate attributeName="r" to="5" dur="1s" repeatCount="indefinite"/></circle></svg></html>`
	status, reason := assessScheduledTestQuality(content, legacyScheduledTestPrompts[0])
	require.NotContains(t, reason, "custom prompt")
	require.NotEqual(t, "", status)

	_, customReason := assessScheduledTestQuality(content, "帮我写一首关于秋天的诗")
	require.Contains(t, customReason, "custom prompt has no quality evaluator")
}
