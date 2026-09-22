package service

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareCloudflareJevUsesNativeInput(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeCloudflare,
		Credentials: map[string]any{
			"account_id": "a1b2c3d4e5f67890",
			"api_key":    "token",
		},
	}
	body := []byte(`{"model":"typesafe/jev","input":{"state":"hello","questions":{"ok":{"type":"noul","instructions":"greeting?","criteria":{"true":"yes","false":"no"}}}}}`)
	url, payload, matched, err := prepareCloudflareJevUpstream(account, body)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, "https://api.cloudflare.com/client/v4/accounts/a1b2c3d4e5f67890/ai/run", url)
	require.Equal(t, "typesafe/jev", gjson.GetBytes(payload, "model").String())
	require.Equal(t, "hello", gjson.GetBytes(payload, "input.state").String())
}

func TestPrepareCloudflareJevReadsUserJSON(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCloudflare, Credentials: map[string]any{"account_id": "a1b2c3d4e5f67890", "api_key": "token"}}
	body := []byte(`{"model":"@cf/typesafe/jev","messages":[{"role":"user","content":"{\"state\":\"s\",\"questions\":{\"q\":{\"type\":\"oul\",\"instructions\":\"pick\",\"criteria\":{\"a\":\"A\"}}}}"}]}`)
	_, payload, matched, err := prepareCloudflareJevUpstream(account, body)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, "s", gjson.GetBytes(payload, "input.state").String())
	require.Equal(t, "oul", gjson.GetBytes(payload, "input.questions.q.type").String())
}

func TestPrepareCloudflareJevRejectsPlainChat(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCloudflare, Credentials: map[string]any{"account_id": "a1b2c3d4e5f67890", "api_key": "token"}}
	_, _, matched, err := prepareCloudflareJevUpstream(account, []byte(`{"model":"typesafe/jev","messages":[{"role":"user","content":"hi"}]}`))
	require.True(t, matched)
	require.Error(t, err)
}

func TestPrepareCloudflareJevIgnoresOtherModels(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeCloudflare, Credentials: map[string]any{"account_id": "a1b2c3d4e5f67890", "api_key": "token"}}
	_, _, matched, err := prepareCloudflareJevUpstream(account, []byte(`{"model":"@cf/meta/llama-3.2-3b-instruct","messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.False(t, matched)
}

func TestAdaptCloudflareJevResponseToChatCompletion(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"success":true,"result":{"ok":true}}`)),
	}
	adapted := adaptCloudflareJevResponse(resp, false, "typesafe/jev")
	body, err := io.ReadAll(adapted.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, adapted.StatusCode)
	require.Equal(t, `{"ok":true}`, gjson.GetBytes(body, "choices.0.message.content").String())
	require.Greater(t, gjson.GetBytes(body, "usage.total_tokens").Int(), int64(0))
}

func TestAdaptCloudflareJevResponseStreamsOneDecision(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"success":true,"result":{"ok":false}}`)),
	}
	adapted := adaptCloudflareJevResponse(resp, true, "typesafe/jev")
	body, err := io.ReadAll(adapted.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "data: [DONE]")
	require.Contains(t, adapted.Header.Get("Content-Type"), "text/event-stream")
	require.Contains(t, string(body), `\"ok\":false`)
}
