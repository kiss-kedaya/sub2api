package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Route real Forward calls by endpoint rather than bypassing the continuation
// with a helper-only assertion. No fixture makes an external network request.
type hostedSearchUpstream struct {
	modelResponses [][]byte
	modelRequests  [][]byte
	searchRequests [][]byte
	searchStatus   int
	searchResponse string
	fetchRequests  [][]byte
	fetchStatus    int
	fetchResponse  string
	fetchContext   func(context.Context)
	modelBody      func(index int, response string) io.ReadCloser
}

func (u *hostedSearchUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	status, contentType, response := http.StatusOK, "application/json", ""
	modelIndex := -1
	if strings.HasSuffix(req.URL.Path, "/tools/search") {
		u.searchRequests = append(u.searchRequests, body)
		status, response = u.searchStatus, u.searchResponse
		if status == 0 {
			status = http.StatusOK
		}
	} else if strings.HasSuffix(req.URL.Path, "/tools/fetch") {
		u.fetchRequests = append(u.fetchRequests, body)
		status, response = u.fetchStatus, u.fetchResponse
		if status == 0 {
			status = http.StatusOK
		}
		if u.fetchContext != nil {
			u.fetchContext(req.Context())
		}
	} else {
		u.modelRequests = append(u.modelRequests, body)
		index := len(u.modelRequests) - 1
		modelIndex = index
		if index >= len(u.modelResponses) {
			return nil, fmt.Errorf("unexpected model continuation %d", index)
		}
		response = string(u.modelResponses[index])
		if gjson.GetBytes(body, "stream").Bool() {
			contentType = "text/event-stream"
			response = hostedSearchSSE(u.modelResponses[index])
		}
	}
	responseBody := io.NopCloser(strings.NewReader(response))
	if modelIndex >= 0 && u.modelBody != nil {
		responseBody = u.modelBody(modelIndex, response)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: responseBody}, nil
}

func (u *hostedSearchUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func hostedSearchResponse(id string, input, output int, items ...map[string]any) []byte {
	body, _ := json.Marshal(map[string]any{
		"id": id, "object": "response", "status": "completed", "created_at": 1793000000,
		"model": "deepseek-v4.1-flash", "output": items,
		"usage": map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output},
	})
	return body
}

func hostedSearchCall() map[string]any {
	return map[string]any{"type": "function_call", "id": "fc_search", "call_id": "call_search", "name": deepSeekHostedSearchToolName, "arguments": `{"query":"DeepSeek search API","count":1}`, "status": "completed"}
}

func hostedSearchAnswer() map[string]any {
	return map[string]any{"type": "message", "id": "msg_answer", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "The search API returns structured web results.", "annotations": []any{}}}}
}

func hostedSearchSSE(response []byte) string {
	var out strings.Builder
	sequence := 0
	emit := func(value map[string]any) {
		value["sequence_number"] = sequence
		sequence++
		encoded, _ := json.Marshal(value)
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", value["type"], encoded)
	}
	id := gjson.GetBytes(response, "id").String()
	emit(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "model": "deepseek-v4.1-flash", "output": []any{}}})
	for index, item := range gjson.GetBytes(response, "output").Array() {
		var object map[string]any
		_ = json.Unmarshal([]byte(item.Raw), &object)
		emit(map[string]any{"type": "response.output_item.added", "output_index": index, "item": object})
		if item.Get("type").String() == "function_call" {
			emit(map[string]any{"type": "response.function_call_arguments.done", "output_index": index, "item_id": item.Get("id").String(), "arguments": item.Get("arguments").String()})
		} else if item.Get("type").String() == "message" {
			emit(map[string]any{"type": "response.output_text.delta", "output_index": index, "content_index": 0, "item_id": item.Get("id").String(), "delta": item.Get("content.0.text").String()})
		}
		emit(map[string]any{"type": "response.output_item.done", "output_index": index, "item": object})
	}
	var object map[string]any
	_ = json.Unmarshal(response, &object)
	emit(map[string]any{"type": "response.completed", "response": object})
	return out.String()
}

func hostedSearchFixture(t *testing.T, upstream *hostedSearchUpstream, stream bool, choice string, mixed bool) (*OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder, *Account, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var tools []any
	// Match the hosted tool emitted by Codex text-and-image search models.
	tools = append(tools, map[string]any{
		"type": "web_search", "search_content_types": []string{"text", "image"},
		"external_web_access": true, "filters": nil, "user_location": nil,
	})
	if mixed {
		tools = append(tools, map[string]any{"type": "function", "name": "client_lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}})
	}
	body, err := json.Marshal(map[string]any{"model": "deepseek-v4.1-flash", "stream": stream, "input": "Look up the DeepSeek search API.", "tools": tools, "tool_choice": choice})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	price, key := 5.0, "independent-search-test-key"
	group := &Group{ID: 36, Platform: PlatformDeepseek, SearchPricePer1k: &price, SearchProviderAPIKey: &key}
	c.Set("api_key", &APIKey{ID: 1, Group: group, GroupID: &group.ID})
	account := &Account{ID: 7, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "model-test-key", "api_protocol": APIProtocolResponses, "base_url": "https://api.deepseek.com"}}
	return &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}, c, recorder, account, body
}

func hostedSearchFinal(t *testing.T, recorder *httptest.ResponseRecorder, stream bool) gjson.Result {
	t.Helper()
	if !stream {
		require.True(t, gjson.Valid(recorder.Body.String()), recorder.Body.String())
		return gjson.Parse(recorder.Body.String())
	}
	var final gjson.Result
	sequence := int64(0)
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") || strings.TrimSpace(strings.TrimPrefix(line, "data: ")) == "[DONE]" {
			continue
		}
		frame := gjson.Parse(strings.TrimPrefix(line, "data: "))
		require.Equal(t, sequence, frame.Get("sequence_number").Int(), line)
		sequence++
		if frame.Get("type").String() == "response.completed" {
			require.False(t, final.Exists(), "only one logical terminal response is allowed")
			final = frame.Get("response")
		}
	}
	require.True(t, final.Exists(), recorder.Body.String())
	return final
}

func TestDeepSeekHostedSearchForwardSearchThenSummary(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_search", 10, 2, hostedSearchCall()),
				hostedSearchResponse("resp_summary", 20, 3, hostedSearchAnswer()),
			}, searchResponse: `{"results":[{"title":"DeepSeek docs","url":"https://example.com/search","summary":"Structured web results"}]}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Len(t, upstream.modelRequests, 2)
			require.Len(t, upstream.searchRequests, 1)
			require.Equal(t, "DeepSeek search API", gjson.GetBytes(upstream.searchRequests[0], "query").String())
			require.Contains(t, string(upstream.modelRequests[1]), "Structured web results")
			require.Contains(t, string(upstream.modelRequests[1]), `"call_id":"call_search"`)
			require.Contains(t, string(upstream.modelRequests[1]), `"type":"function_call_output"`)
			require.Equal(t, 1, result.SearchCount)
			require.Zero(t, result.WebSearchCalls, "hosted search must add a surcharge without replacing token billing")
			require.Equal(t, 30, result.Usage.InputTokens)
			require.Equal(t, 5, result.Usage.OutputTokens)
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "resp_search", final.Get("id").String())
			require.Equal(t, "web_search_call", final.Get("output.0.type").String())
			require.Equal(t, "completed", final.Get("output.0.status").String())
			require.Equal(t, "message", final.Get("output.1.type").String())
			require.Contains(t, final.Get("output.1.content.0.text").String(), "structured web results")
			require.Equal(t, int64(30), final.Get("usage.input_tokens").Int())
			require.Equal(t, int64(5), final.Get("usage.output_tokens").Int())
			require.NotContains(t, recorder.Body.String(), `"name":"`+deepSeekHostedSearchToolName+`"`)
			if stream {
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					frame := gjson.Parse(strings.TrimPrefix(line, "data: "))
					if frame.Get("type").String() == "response.output_text.delta" {
						require.Equal(t, int64(1), frame.Get("output_index").Int(), "second-round index 0 must follow the first-round search item")
					}
				}
			}
		})
	}
}

func TestDeepSeekHostedSearchNoneAndAutoWithoutSearchDoNotCallProvider(t *testing.T) {
	for _, choice := range []string{"none", "auto"} {
		t.Run(choice, func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_direct", 10, 2, hostedSearchAnswer())}}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, false, choice, false)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Len(t, upstream.modelRequests, 1)
			require.Empty(t, upstream.searchRequests)
			require.Zero(t, result.SearchCount)
			require.Zero(t, result.WebSearchCalls)
		})
	}
}

func TestDeepSeekHostedSearchProviderFailureIsNotBilled(t *testing.T) {
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{
		hostedSearchResponse("resp_search", 10, 2, hostedSearchCall()), hostedSearchResponse("resp_summary", 20, 3, hostedSearchAnswer()),
	}, searchStatus: http.StatusUnauthorized, searchResponse: `{"error":{"message":"private provider diagnostic"}}`}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.NotNil(t, result)
	require.Len(t, upstream.searchRequests, 1)
	require.Len(t, upstream.modelRequests, 2)
	require.Zero(t, result.SearchCount)
	require.Zero(t, result.WebSearchCalls)
	require.Equal(t, 30, result.Usage.InputTokens)
	require.Contains(t, string(upstream.modelRequests[1]), "search provider request failed")
	require.NotContains(t, recorder.Body.String(), "private provider diagnostic")
	final := hostedSearchFinal(t, recorder, true)
	require.Equal(t, "failed", final.Get("output.0.status").String())
}

func TestDeepSeekHostedSearchMixedClientToolDoesNotContinueWithOrphanCall(t *testing.T) {
	clientCall := map[string]any{"type": "function_call", "id": "fc_client", "call_id": "call_client", "name": "client_lookup", "arguments": `{}`, "status": "completed"}
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_mixed", 10, 2, hostedSearchCall(), clientCall)},
		searchResponse: `{"results":[{"title":"DeepSeek docs","url":"https://example.com/search","summary":"Useful source"}]}`}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", true)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.NotNil(t, result)
	require.Len(t, upstream.modelRequests, 1, "client-owned output is not yet available for a continuation")
	require.Len(t, upstream.searchRequests, 1)
	require.Equal(t, 1, result.SearchCount)
	final := hostedSearchFinal(t, recorder, true)
	require.Equal(t, "web_search_call", final.Get("output.0.type").String())
	require.Equal(t, "function_call", final.Get("output.1.type").String())
	require.Equal(t, "call_client", final.Get("output.1.call_id").String())
	require.Equal(t, "client_lookup", final.Get("output.1.name").String())
}

// Only release the second round's terminal bytes once the earlier text has
// reached the downstream writer. Reading the whole model response first would
// fail the assertion while still allowing this test to exit without deadlock.
type hostedSearchChunkReader struct {
	chunks     []string
	beforeTail func()
	tailRead   bool
}

func (r *hostedSearchChunkReader) Read(p []byte) (int, error) {
	for len(r.chunks) > 0 && r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	if len(r.chunks) == 1 && !r.tailRead {
		r.tailRead = true
		if r.beforeTail != nil {
			r.beforeTail()
		}
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	return n, nil
}

func (r *hostedSearchChunkReader) Close() error { return nil }

type hostedSearchObservedWriter struct {
	gin.ResponseWriter
	textWritten chan struct{}
	once        sync.Once
}

func (w *hostedSearchObservedWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 && bytes.Contains(p[:n], []byte("structured web results")) {
		w.once.Do(func() { close(w.textWritten) })
	}
	return n, err
}

func TestDeepSeekHostedSearchStreamsBeforeFinalModelBytesAndHandlesCRLFChunks(t *testing.T) {
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{
		hostedSearchResponse("resp_search", 10, 2, hostedSearchCall()), hostedSearchResponse("resp_summary", 20, 3, hostedSearchAnswer()),
	}, searchResponse: `{"results":[{"title":"DeepSeek docs","url":"https://example.com/search","summary":"Useful source"}]}`}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", false)
	observed := &hostedSearchObservedWriter{ResponseWriter: c.Writer, textWritten: make(chan struct{})}
	c.Writer = observed
	var tailChecked atomic.Bool
	upstream.modelBody = func(index int, response string) io.ReadCloser {
		response = strings.ReplaceAll(response, "\n", "\r\n")
		terminal := strings.Index(response, "event: response.completed")
		require.GreaterOrEqual(t, terminal, 0)
		prefix, tail := response[:terminal], response[terminal:]
		var chunks []string
		for len(prefix) > 0 {
			end := 7
			if len(prefix) < end {
				end = len(prefix)
			}
			chunks = append(chunks, prefix[:end])
			prefix = prefix[end:]
		}
		chunks = append(chunks, tail)
		reader := &hostedSearchChunkReader{chunks: chunks}
		if index == 1 {
			reader.beforeTail = func() {
				select {
				case <-observed.textWritten:
					tailChecked.Store(true)
				case <-time.After(2 * time.Second):
					// Release the tail even on failure so scanner cleanup cannot hang.
				}
			}
		}
		return reader
	}
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.True(t, tailChecked.Load(), "text must be forwarded before the terminal bytes are available")
	require.NotNil(t, result.FirstTokenMs)
	require.Equal(t, 1, result.SearchCount)
	final := hostedSearchFinal(t, recorder, true)
	require.Equal(t, "message", final.Get("output.1.type").String())
}
