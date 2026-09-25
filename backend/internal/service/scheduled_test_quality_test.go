package service

import "testing"

func TestScheduledTestQualityFailure(t *testing.T) {
	valid := `<!doctype html><html><body><svg viewBox="0 0 10 10"><circle><animate attributeName="cx" repeatCount="indefinite" /></circle></svg></body></html>`
	cases := []struct {
		name     string
		response string
		wantFail bool
	}{
		{name: "complete animated document", response: valid},
		{name: "empty", response: "", wantFail: true},
		{name: "code fence", response: "```html\n" + valid + "\n```", wantFail: true},
		{name: "missing animation", response: `<!doctype html><html><body><svg></svg></body></html>`, wantFail: true},
		{name: "external resource", response: `<!doctype html><html><body><svg></svg><script src="https://example.com/app.js"></script></body></html>`, wantFail: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			failed := scheduledTestQualityFailure(testCase.response) != ""
			if failed != testCase.wantFail {
				t.Fatalf("quality failure = %v, want %v", failed, testCase.wantFail)
			}
		})
	}
}

func TestScheduledTestPromptIsForwarded(t *testing.T) {
	prompt := "render a complete animated HTML document"
	claudePayload, err := createTestPayload("claude-test", prompt)
	if err != nil {
		t.Fatalf("create Claude payload: %v", err)
	}
	claudeMessages, ok := claudePayload["messages"].([]map[string]any)
	if !ok || len(claudeMessages) == 0 {
		t.Fatalf("claude payload messages missing or malformed: %#v", claudePayload["messages"])
	}
	claudeContent, ok := claudeMessages[0]["content"].([]map[string]any)
	if !ok || len(claudeContent) == 0 {
		t.Fatalf("claude message content missing or malformed: %#v", claudeMessages[0]["content"])
	}
	if got := claudeContent[0]["text"]; got != prompt {
		t.Fatalf("Claude prompt = %v, want %q", got, prompt)
	}

	responsesPayload := createOpenAITestPayload("responses-test", false, prompt)
	responsesInput, ok := responsesPayload["input"].([]map[string]any)
	if !ok || len(responsesInput) == 0 {
		t.Fatalf("responses payload input missing or malformed: %#v", responsesPayload["input"])
	}
	responsesContent, ok := responsesInput[0]["content"].([]map[string]any)
	if !ok || len(responsesContent) == 0 {
		t.Fatalf("responses input content missing or malformed: %#v", responsesInput[0]["content"])
	}
	if got := responsesContent[0]["text"]; got != prompt {
		t.Fatalf("Responses prompt = %v, want %q", got, prompt)
	}
}
