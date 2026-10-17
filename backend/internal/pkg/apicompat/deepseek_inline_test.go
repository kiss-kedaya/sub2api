package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractDeepSeekInline_ThinkingAndOrphanClosers(t *testing.T) {
	visible, reasoning, calls := ExtractDeepSeekInline("<thinking>plan</thinking>Let me run.</\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter>\n</\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke>\n</\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>")
	assert.Equal(t, "plan", reasoning)
	assert.Equal(t, "Let me run.", visible)
	assert.Empty(t, calls)
	assert.NotContains(t, visible, "DSML")
	assert.NotContains(t, visible, "thinking>")
}

func TestExtractDeepSeekInline_MangledToolCall(t *testing.T) {
	raw := "Look.\n<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>\n<\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke name=\"bash\">\n<\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter name=\"command\" string=\"true\">ls -la</\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter>\n</\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke>\n</\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>"
	visible, _, calls := ExtractDeepSeekInline(raw)
	assert.Equal(t, "Look.", visible)
	require.Len(t, calls, 1)
	assert.Equal(t, "bash", calls[0].Name)
	assert.JSONEq(t, `{"command":"ls -la"}`, calls[0].Arguments)
}

func TestExtractDeepSeekInline_CanonicalJSONParam(t *testing.T) {
	raw := "<\uFF5CDSML\uFF5C calls><\uFF5CDSML\uFF5C invoke name=\"exec\"><\uFF5CDSML\uFF5C parameter name=\"command\" string=\"false\">[\"pwd\"]</\uFF5CDSML\uFF5C parameter></\uFF5CDSML\uFF5C invoke></\uFF5CDSML\uFF5C calls>"
	visible, _, calls := ExtractDeepSeekInline(raw)
	assert.Empty(t, visible)
	require.Len(t, calls, 1)
	assert.Equal(t, "exec", calls[0].Name)
	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(calls[0].Arguments), &args))
	assert.Equal(t, []any{"pwd"}, args["command"])
}

func TestDeepSeekInlineFilter_ChunkIndependent(t *testing.T) {
	raw := "<thinking>a</thinking>hi<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls><\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke name=\"bash\"><\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter name=\"command\" string=\"true\">pwd</\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter></\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke></\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>"
	var filter DeepSeekInlineFilter
	var got DeepSeekPiece
	for _, part := range strings.Split(raw, "") {
		piece := filter.Push(part)
		got.Text += piece.Text
		got.Reasoning += piece.Reasoning
		got.Calls = append(got.Calls, piece.Calls...)
	}
	tail := filter.Flush()
	got.Text += tail.Text
	got.Reasoning += tail.Reasoning
	got.Calls = append(got.Calls, tail.Calls...)
	assert.Equal(t, "a", got.Reasoning)
	assert.Equal(t, "hi", got.Text)
	require.Len(t, got.Calls, 1)
	assert.JSONEq(t, `{"command":"pwd"}`, got.Calls[0].Arguments)
}

// 回归：流在工具调用块中途结束（上游截断 / 断连）时，未闭合的 DSML 不能回流
// 成可见正文。以前 Flush 在 parseDSMLBlock 拿不到调用时会把整段残留塞进
// Text，用户就会在回复里看到 <｜｜DSML｜｜ parameter> 这类原始标记。
func TestExtractDeepSeekInline_UnterminatedDSMLNeverBecomesVisible(t *testing.T) {
	raw := "Running.\n<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>\n<\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke name=\"bash\">\n<\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter name=\"command\" string=\"true\">ls -la"
	visible, _, calls := ExtractDeepSeekInline(raw)
	assert.Equal(t, "Running.", visible)
	assert.NotContains(t, visible, "DSML")
	// 已经闭合的 invoke 仍然要救回来，别把可用调用一起丢。
	require.Len(t, calls, 1)
	assert.JSONEq(t, `{"command":"ls -la"}`, calls[0].Arguments)
}

// 只有开标签、没有可解析调用的场景：以前 parseDSMLBlock 返回空会把整段
// 原始标记塞进可见正文，这是用户实际看到的泄漏。
func TestExtractDeepSeekInline_BareDSMLOpenerDropped(t *testing.T) {
	visible, _, calls := ExtractDeepSeekInline("Hi.<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>")
	assert.NotContains(t, visible, "DSML")
	assert.Equal(t, "Hi.", visible)
	assert.Empty(t, calls)
}

// 回归：DSML 块超过 dsmlMaxHold 时同样只能丢弃，不能转成可见正文。
func TestDeepSeekInlineFilter_DSMLOverflowNeverBecomesVisible(t *testing.T) {
	var filter DeepSeekInlineFilter
	var got DeepSeekPiece

	open := "<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls><\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke name=\"bash\"><\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter name=\"command\" string=\"true\">"
	piece := filter.Push("go\n" + open)
	got.Text += piece.Text

	piece = filter.Push(strings.Repeat("A", dsmlMaxHold+1024))
	got.Text += piece.Text
	got.Calls = append(got.Calls, piece.Calls...)

	tail := filter.Flush()
	got.Text += tail.Text
	got.Calls = append(got.Calls, tail.Calls...)

	assert.Equal(t, "go", got.Text)
	assert.NotContains(t, got.Text, "DSML")
}

func TestStream_DeepSeekDSMLBecomesFunctionCall(t *testing.T) {
	raw := "Look.<\uFF5C\uFF5CDSML\uFF5C\uFF5C calls><\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke name=\"bash\"><\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter name=\"command\" string=\"true\">pwd</\uFF5C\uFF5CDSML\uFF5C\uFF5C parameter></\uFF5C\uFF5CDSML\uFF5C\uFF5C invoke></\uFF5C\uFF5CDSML\uFF5C\uFF5C calls>"
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"content":` + jsonString(raw) + `}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})
	var text strings.Builder
	var sawCall bool
	for _, event := range events {
		switch event.Type {
		case "response.output_text.delta":
			_, _ = text.WriteString(event.Delta)
			assert.NotContains(t, event.Delta, "DSML")
		case "response.output_item.done":
			if event.Item != nil && (event.Item.Type == "function_call" || event.Item.Type == "local_shell_call") && event.Item.Name == "bash" {
				sawCall = true
				assert.JSONEq(t, `{"command":"pwd"}`, event.Item.Arguments)
			}
		}
	}
	assert.Equal(t, "Look.", text.String())
	assert.True(t, sawCall)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestExtractDeepSeekInline_AnalysisAndSummaryLeaveBody(t *testing.T) {
	raw := "look.\n\n<analysis>\nplan</analysis>\n\n<summary>\n1. next\n</summary>"
	visible, reasoning, calls := ExtractDeepSeekInline(raw)
	assert.Empty(t, calls)
	assert.Contains(t, visible, "look.")
	assert.NotContains(t, visible, "<analysis>")
	assert.NotContains(t, visible, "<summary>")
	assert.NotContains(t, visible, "plan")
	assert.Contains(t, reasoning, "plan")
	assert.Contains(t, reasoning, "1. next")
}
