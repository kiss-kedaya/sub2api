package apicompat

import "testing"

// 转换层不允许把 max_output_tokens 发得比配置下限更小，否则上游直接 400。
func TestMinOutputTokensDefaultAndClamp(t *testing.T) {
	restore := MinOutputTokens()
	t.Cleanup(func() { SetMinOutputTokens(restore) })

	SetMinOutputTokens(0)
	if got := MinOutputTokens(); got != MinAllowedOutputTokens {
		t.Fatalf("SetMinOutputTokens(0) => %d, want clamp to %d", got, MinAllowedOutputTokens)
	}
	SetMinOutputTokens(32)
	if got := MinOutputTokens(); got != 32 {
		t.Fatalf("SetMinOutputTokens(32) => %d, want 32", got)
	}
	if DefaultMinMaxOutputTokens != minMaxOutputTokens {
		t.Fatalf("default constant drift: %d vs %d", DefaultMinMaxOutputTokens, minMaxOutputTokens)
	}
}

func TestConversionHonoursConfiguredFloor(t *testing.T) {
	restore := MinOutputTokens()
	t.Cleanup(func() { SetMinOutputTokens(restore) })
	SetMinOutputTokens(256)

	resp, err := AnthropicToResponses(&AnthropicRequest{Model: "gpt-5.1", MaxTokens: 10})
	if err != nil {
		t.Fatalf("AnthropicToResponses: %v", err)
	}
	if resp.MaxOutputTokens == nil || *resp.MaxOutputTokens != 256 {
		t.Fatalf("max_output_tokens = %v, want 256", resp.MaxOutputTokens)
	}

	chat, err := AnthropicToChatCompletionsRequest(&AnthropicRequest{Model: "gpt-5.1", MaxTokens: 10})
	if err != nil {
		t.Fatalf("AnthropicToChatCompletionsRequest: %v", err)
	}
	if chat.MaxCompletionTokens == nil || *chat.MaxCompletionTokens != 256 {
		t.Fatalf("max_completion_tokens = %v, want 256", chat.MaxCompletionTokens)
	}

	// 高于下限的值保持原样，不能被抬高。
	respBig, err := AnthropicToResponses(&AnthropicRequest{Model: "gpt-5.1", MaxTokens: 4096})
	if err != nil {
		t.Fatalf("AnthropicToResponses(big): %v", err)
	}
	if respBig.MaxOutputTokens == nil || *respBig.MaxOutputTokens != 4096 {
		t.Fatalf("max_output_tokens = %v, want 4096", respBig.MaxOutputTokens)
	}
}
