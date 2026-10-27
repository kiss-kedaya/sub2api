package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func hostedFetchCall(id, pageURL string) map[string]any {
	arguments, _ := json.Marshal(map[string]string{"url": pageURL})
	return map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id,
		"name": deepSeekHostedFetchToolName, "arguments": string(arguments), "status": "completed"}
}

func hostedFetchOutput(t *testing.T, body []byte, callID string) gjson.Result {
	t.Helper()
	var output gjson.Result
	count := 0
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("type").String() == "function_call_output" && item.Get("call_id").String() == callID {
			count++
			output = gjson.Parse(item.Get("output").String())
		}
	}
	require.Equal(t, 1, count, "each owned call needs exactly one continuation output")
	require.True(t, output.IsObject())
	return output
}

func TestDeepSeekHostedFetchSearchThenPageThenSummary(t *testing.T) {
	const pageURL = "https://example.com/deepseek-search"
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_search", 10, 2, hostedSearchCall()),
				hostedSearchResponse("resp_fetch", 20, 3, hostedFetchCall("open_docs", pageURL)),
				hostedSearchResponse("resp_summary", 30, 4, hostedSearchAnswer()),
			}, searchResponse: `{"results":[{"title":"DeepSeek docs","url":"` + pageURL + `","summary":"Search snippet"}]}`,
				fetchResponse: `{"title":"DeepSeek docs","content":"Full page content beyond the search snippet."}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
			// Concise web_search matches the legacy emulation name. Even with
			// that opt-in compatibility flag, only the hosted loop may execute it.
			svc.cfg.Gateway.DeepSeekResponseCompatEnabled = true
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Len(t, upstream.modelRequests, 3)
			require.Len(t, upstream.searchRequests, 1)
			require.Len(t, upstream.fetchRequests, 1)
			require.JSONEq(t, `{"url":"`+pageURL+`"}`, string(upstream.fetchRequests[0]), "Fetch accepts only its URL, never search query/count")
			require.Equal(t, "Full page content beyond the search snippet.", hostedFetchOutput(t, upstream.modelRequests[2], "open_docs").Get("content").String())
			require.Equal(t, 2, result.SearchCount)
			require.Zero(t, result.WebSearchCalls, "tool surcharges must not bypass model token billing")
			require.Equal(t, 60, result.Usage.InputTokens)
			require.Equal(t, 9, result.Usage.OutputTokens)
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "resp_search", final.Get("id").String())
			require.Len(t, final.Get("output").Array(), 3)
			require.Equal(t, "search", final.Get("output.0.action.type").String())
			require.Equal(t, "web_search_call", final.Get("output.1.type").String())
			require.Equal(t, "completed", final.Get("output.1.status").String())
			require.Equal(t, "open_page", final.Get("output.1.action.type").String())
			require.Equal(t, pageURL, final.Get("output.1.action.url").String())
			require.False(t, final.Get("output.1.action.sources").Exists(), "opening a page is not a new search result list")
			require.False(t, final.Get("output.1.action.query").Exists())
			require.Equal(t, "message", final.Get("output.2.type").String())
			require.NotContains(t, recorder.Body.String(), `"name":"`+deepSeekHostedFetchToolName+`"`)
			require.NotContains(t, recorder.Body.String(), `"name":"`+deepSeekHostedSearchToolName+`"`)
			if stream {
				for _, line := range strings.Split(recorder.Body.String(), "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					frame := gjson.Parse(strings.TrimPrefix(line, "data: "))
					if frame.Get("type").String() == "response.output_text.delta" {
						require.Equal(t, int64(2), frame.Get("output_index").Int())
					}
				}
			}
		})
	}
}

func TestDeepSeekHostedFetchDirectURLNeedsNoSearchCall(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_page", 11, 2, hostedFetchCall("open_direct", "https://example.com/page")),
				hostedSearchResponse("resp_answer", 21, 3, hostedSearchAnswer()),
			}, fetchResponse: `{"content":"Public page requested directly."}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Empty(t, upstream.searchRequests)
			require.Len(t, upstream.fetchRequests, 1)
			require.Len(t, upstream.modelRequests, 2)
			require.Equal(t, "Public page requested directly.", hostedFetchOutput(t, upstream.modelRequests[1], "open_direct").Get("content").String())
			require.Equal(t, 1, result.SearchCount)
			require.Equal(t, 32, result.Usage.InputTokens)
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "open_page", final.Get("output.0.action.type").String())
		})
	}
}

func TestDeepSeekHostedFetchProviderFailureAndEmptyAreNotBilled(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		response string
	}{
		{"forbidden", http.StatusForbidden, `{"error":{"message":"private-provider-diagnostic"}}`},
		{"busy", http.StatusServiceUnavailable, `{"content":"must not bill error content"}`},
		{"empty", http.StatusOK, `{"content":""}`},
		{"whitespace", http.StatusOK, `{"content":" \n\t"}`},
		{"null", http.StatusOK, `{"content":null}`},
		{"wrong type", http.StatusOK, `{"content":["not page text"]}`},
		{"malformed", http.StatusOK, `{"content":`},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				upstream := &hostedSearchUpstream{modelResponses: [][]byte{
					hostedSearchResponse("resp_page", 10, 2, hostedFetchCall("open_failed", "https://example.com/page")),
					hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
				}, fetchStatus: tc.status, fetchResponse: tc.response}
				svc, c, recorder, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err, recorder.Body.String())
				require.NotNil(t, result)
				require.Len(t, upstream.fetchRequests, 1)
				require.Len(t, upstream.modelRequests, 2)
				require.Zero(t, result.SearchCount)
				require.Zero(t, result.WebSearchCalls)
				require.Equal(t, 30, result.Usage.InputTokens, "provider failure must not discard successful model usage")
				require.NotEmpty(t, hostedFetchOutput(t, upstream.modelRequests[1], "open_failed").Get("error").String())
				require.NotContains(t, string(upstream.modelRequests[1]), "private-provider-diagnostic")
				require.NotContains(t, recorder.Body.String(), "private-provider-diagnostic")
				final := hostedSearchFinal(t, recorder, stream)
				require.Equal(t, "failed", final.Get("output.0.status").String())
			})
		}
	}
}

func TestDeepSeekHostedFetchRejectsUnsafeURLsBeforeProvider(t *testing.T) {
	for _, pageURL := range []string{
		"http://127.0.0.1/admin", "http://10.0.0.1/", "http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.200/", "http://192.0.2.1/", "http://198.18.0.1/", "http://224.0.0.1/",
		"http://[::1]/", "http://[fd00::1]/", "http://[fe80::1]/", "http://[::ffff:127.0.0.1]/",
		"http://127.1/", "http://2130706433/", "http://0x7f.0x0.0x0.0x1/",
		"https://localhost/", "https://private.internal/", "https://user:secret@example.com/",
		"https://example.com:99999/", "https://example.com:abc/", "file:///etc/passwd", "ftp://example.com/page",
	} {
		t.Run(pageURL, func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_bad_url", 10, 2, hostedFetchCall("open_bad", pageURL)),
				hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
			}}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, false, "auto", false)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Empty(t, upstream.fetchRequests, "unsafe user/model URL must never reach the provider")
			require.Zero(t, result.SearchCount)
			require.Len(t, upstream.modelRequests, 2)
			require.NotEmpty(t, hostedFetchOutput(t, upstream.modelRequests[1], "open_bad").Get("error").String())
		})
	}
}

func TestDeepSeekHostedFetchHonorsAllowedDomains(t *testing.T) {
	for _, tc := range []struct {
		pageURL string
		allowed bool
	}{
		{"https://example.com/page", true},
		{"https://docs.example.com/page", true},
		{"https://EXAMPLE.COM/page", true},
		{"https://evil.example.org/page", false},
		{"https://example.com.evil.org/page", false},
		{"https://notexample.com/page", false},
	} {
		t.Run(tc.pageURL, func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_filtered", 10, 2, hostedFetchCall("open_filtered", tc.pageURL)),
				hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
			}, fetchResponse: `{"content":"Allowed public page."}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, false, "auto", false)
			body, err := sjson.SetBytes(body, "tools.0.filters", map[string]any{"allowed_domains": []string{"example.com"}})
			require.NoError(t, err)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			if tc.allowed {
				require.Len(t, upstream.fetchRequests, 1)
				require.Equal(t, 1, result.SearchCount)
				require.False(t, hostedFetchOutput(t, upstream.modelRequests[1], "open_filtered").Get("error").Exists())
			} else {
				require.Empty(t, upstream.fetchRequests)
				require.Zero(t, result.SearchCount)
				require.Contains(t, hostedFetchOutput(t, upstream.modelRequests[1], "open_filtered").Get("error").String(), "allowed domains")
			}
		})
	}
}

func TestDeepSeekHostedFetchBoundsPageContentWithoutBreakingUTF8(t *testing.T) {
	content := strings.Repeat("界", (64<<10)/3+100) + "UNTRUSTED_TAIL_MUST_BE_OMITTED"
	response, err := json.Marshal(map[string]string{"title": "Large page", "content": content})
	var upstream = &hostedSearchUpstream{modelResponses: [][]byte{
		hostedSearchResponse("resp_large", 10, 2, hostedFetchCall("open_large", "https://example.com/large")),
		hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
	}, fetchResponse: string(response)}
	require.NoError(t, err)
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, false, "auto", false)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.NotNil(t, result)
	require.Len(t, upstream.modelRequests, 2)
	output := hostedFetchOutput(t, upstream.modelRequests[1], "open_large")
	require.True(t, output.Get("truncated").Bool())
	page := output.Get("content").String()
	require.LessOrEqual(t, len(page), 64<<10)
	require.Greater(t, len(page), (64<<10)-4)
	require.True(t, utf8.ValidString(page))
	require.True(t, strings.HasPrefix(content, page))
	require.NotContains(t, string(upstream.modelRequests[1]), "UNTRUSTED_TAIL_MUST_BE_OMITTED")
	require.Equal(t, 1, result.SearchCount, "successful provider page is billed once even if continuation content is shortened")
}

type hostedFetchReadCounter struct {
	io.ReadCloser
	bytesRead int
}

func (r *hostedFetchReadCounter) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytesRead += n
	return n, err
}

type hostedFetchLimitedUpstream struct {
	*hostedSearchUpstream
	reader *hostedFetchReadCounter
}

func (u *hostedFetchLimitedUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	response, err := u.hostedSearchUpstream.Do(req, proxy, id, concurrency)
	if err == nil && strings.HasSuffix(req.URL.Path, "/tools/fetch") {
		u.reader = &hostedFetchReadCounter{ReadCloser: response.Body}
		response.Body = u.reader
	}
	return response, err
}

func (u *hostedFetchLimitedUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestDeepSeekHostedFetchRejectsOversizedProviderResponseBeforeReadingAll(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			base := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_oversized", 10, 2, hostedFetchCall("open_oversized", "https://example.com/oversized")),
				hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
			}, fetchResponse: `{"content":"` + strings.Repeat("x", 2<<20) + `"}`}
			upstream := &hostedFetchLimitedUpstream{hostedSearchUpstream: base}
			svc, c, recorder, account, body := hostedSearchFixture(t, base, stream, "auto", false)
			svc.httpUpstream = upstream
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, upstream.reader)
			require.LessOrEqual(t, upstream.reader.bytesRead, (1<<20)+1, "provider response must be capped while reading")
			require.NotNil(t, result)
			require.Zero(t, result.SearchCount)
			require.Len(t, base.fetchRequests, 1)
			require.Len(t, base.modelRequests, 2)
			require.NotEmpty(t, hostedFetchOutput(t, base.modelRequests[1], "open_oversized").Get("error").String())
			final := hostedSearchFinal(t, recorder, stream)
			require.Equal(t, "failed", final.Get("output.0.status").String())
		})
	}
}

func TestDeepSeekHostedFetchHistoryReplaysWithoutCallingProviderAgain(t *testing.T) {
	const pageURL = "https://example.com/history"
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{
		hostedSearchResponse("resp_history", 10, 2, hostedFetchCall("open_history", pageURL)),
		hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
	}, fetchResponse: `{"content":"Previously opened page."}`}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, false, "auto", false)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.Equal(t, 1, result.SearchCount)
	final := hostedSearchFinal(t, recorder, false)
	var history []json.RawMessage
	for _, item := range final.Get("output").Array() {
		history = append(history, json.RawMessage(item.Raw))
	}
	history = append(history, json.RawMessage(`{"role":"user","content":"Explain the previous page again without reopening it."}`))
	second := &hostedSearchUpstream{modelResponses: [][]byte{hostedSearchResponse("resp_followup", 15, 2, hostedSearchAnswer())}}
	svc2, c2, recorder2, account2, body2 := hostedSearchFixture(t, second, false, "none", false)
	body2, err = sjson.SetBytes(body2, "input", history)
	require.NoError(t, err)
	result, err = svc2.Forward(context.Background(), c2, account2, body2)
	require.NoError(t, err, recorder2.Body.String())
	require.NotNil(t, result)
	require.Zero(t, result.SearchCount)
	require.Empty(t, second.searchRequests)
	require.Empty(t, second.fetchRequests)
	require.Len(t, second.modelRequests, 1)
	replayed := gjson.GetBytes(second.modelRequests[0], "input.0")
	require.Equal(t, "message", replayed.Get("type").String())
	require.Contains(t, replayed.Get("content.0.text").String(), pageURL)
	require.NotContains(t, string(second.modelRequests[0]), deepSeekHostedFetchToolName)
	require.NotContains(t, string(second.modelRequests[0]), `"type":"web_search_call"`)
}

func TestDeepSeekHostedFetchSharesSearchBudgetAndKeepsClientTools(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprintf("client_tools=%t", mixed), func(t *testing.T) {
			upstream := &hostedSearchUpstream{modelResponses: [][]byte{
				hostedSearchResponse("resp_budget", 10, 2,
					hostedSearchCall(), hostedFetchCall("open_1", "https://example.com/1"),
					hostedFetchCall("open_2", "https://example.com/2"), hostedFetchCall("open_over_budget", "https://example.com/3")),
				hostedSearchResponse("resp_answer", 20, 3, hostedSearchAnswer()),
			}, searchResponse: `{"results":[{"url":"https://example.com/1","summary":"Page snippet"}]}`,
				fetchResponse: `{"content":"Page content."}`}
			svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", mixed)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err, recorder.Body.String())
			require.NotNil(t, result)
			require.Len(t, upstream.searchRequests, 1)
			require.Len(t, upstream.fetchRequests, 2)
			require.Equal(t, 3, result.SearchCount, "search and fetch share the same paid-tool budget")
			require.Len(t, upstream.modelRequests, 2)
			require.Contains(t, hostedFetchOutput(t, upstream.modelRequests[1], "open_over_budget").Get("error").String(), "limit")
			tools := gjson.GetBytes(upstream.modelRequests[1], "tools").Array()
			if mixed {
				require.Len(t, tools, 1)
				require.Equal(t, "client_lookup", tools[0].Get("name").String())
			} else {
				require.Empty(t, tools)
			}
			for _, tool := range tools {
				require.NotEqual(t, deepSeekHostedFetchToolName, tool.Get("name").String())
				require.NotEqual(t, deepSeekHostedSearchToolName, tool.Get("name").String())
			}
			final := hostedSearchFinal(t, recorder, true)
			require.Equal(t, "failed", final.Get("output.3.status").String())
		})
	}
}

func TestDeepSeekHostedFetchMixedClientCallDoesNotContinueWithMissingResult(t *testing.T) {
	client := map[string]any{"type": "function_call", "id": "fc_local", "call_id": "call_local", "name": "client_lookup", "arguments": `{}`, "status": "completed"}
	upstream := &hostedSearchUpstream{modelResponses: [][]byte{
		hostedSearchResponse("resp_mixed", 10, 2, hostedFetchCall("open_mixed", "https://example.com/page"), client),
	}, fetchResponse: `{"content":"A public page."}`}
	svc, c, recorder, account, body := hostedSearchFixture(t, upstream, true, "auto", true)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err, recorder.Body.String())
	require.Len(t, upstream.fetchRequests, 1)
	require.Len(t, upstream.modelRequests, 1, "client owns the missing client_lookup result")
	require.Equal(t, 1, result.SearchCount)
	final := hostedSearchFinal(t, recorder, true)
	require.Equal(t, "open_page", final.Get("output.0.action.type").String())
	require.Equal(t, "function_call", final.Get("output.1.type").String())
	require.Equal(t, "call_local", final.Get("output.1.call_id").String())
	require.Equal(t, "client_lookup", final.Get("output.1.name").String())
}

func TestDeepSeekHostedFetchCancellationStopsNewWork(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, phase := range []string{"before_model", "before_fetch", "after_fetch"} {
			t.Run(fmt.Sprintf("%s/stream=%t", phase, stream), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				upstream := &hostedSearchUpstream{modelResponses: [][]byte{
					hostedSearchResponse("resp_canceled", 10, 2, hostedFetchCall("open_canceled", "https://example.com/page")),
				}, fetchResponse: `{"content":"Successfully fetched before cancellation."}`}
				switch phase {
				case "before_model":
					cancel()
				case "before_fetch":
					upstream.modelBody = func(_ int, response string) io.ReadCloser {
						return &hostedSearchChunkReader{chunks: []string{response}, beforeTail: cancel}
					}
				default:
					upstream.fetchContext = func(fetchCtx context.Context) {
						require.NoError(t, fetchCtx.Err())
						_, deadlineSet := fetchCtx.Deadline()
						require.True(t, deadlineSet, "provider operation must retain a finite deadline")
						cancel()
					}
				}
				svc, c, _, account, body := hostedSearchFixture(t, upstream, stream, "auto", false)
				c.Request = c.Request.WithContext(ctx)
				result, err := svc.Forward(ctx, c, account, body)
				require.Error(t, err)
				require.NotNil(t, result)
				require.True(t, result.ClientDisconnect)
				require.Empty(t, upstream.searchRequests)
				if phase == "before_model" {
					require.Empty(t, upstream.modelRequests)
				} else {
					require.Len(t, upstream.modelRequests, 1, "cancellation must stop before a model continuation")
				}
				if phase == "after_fetch" {
					require.Len(t, upstream.fetchRequests, 1)
					require.Equal(t, 1, result.SearchCount, "completed paid provider work remains billable after cancellation")
				} else {
					require.Empty(t, upstream.fetchRequests)
					require.Zero(t, result.SearchCount)
				}
			})
		}
	}
}

func TestPrepareDeepSeekHostedFetchSchemaPreservesClientTools(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"local_before","parameters":{}},{"type":"web_search"},{"type":"function","name":"local_after","parameters":{}}],"input":"Open the supplied URL"}`)
	next, changed, err := prepareDeepSeekHostedSearchRequest(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, gjson.GetBytes(next, "tools").Array(), 4)
	require.JSONEq(t, gjson.GetBytes(body, "tools.0").Raw, gjson.GetBytes(next, "tools.0").Raw)
	require.Equal(t, deepSeekHostedSearchToolName, gjson.GetBytes(next, "tools.1.name").String())
	require.Equal(t, "web_search", gjson.GetBytes(next, "tools.1.name").String())
	require.Contains(t, gjson.GetBytes(next, "tools.1.description").String(), `Call with {"query":`)
	require.Contains(t, gjson.GetBytes(next, "tools.1.description").String(), "web_fetch")
	require.JSONEq(t, gjson.GetBytes(body, "tools.2").Raw, gjson.GetBytes(next, "tools.2").Raw)
	fetch := gjson.GetBytes(next, "tools.3")
	require.Equal(t, "function", fetch.Get("type").String())
	require.Equal(t, deepSeekHostedFetchToolName, fetch.Get("name").String())
	require.Equal(t, "web_fetch", fetch.Get("name").String())
	require.Contains(t, fetch.Get("description").String(), `Call with {"url":`)
	require.Contains(t, fetch.Get("description").String(), "Markdown content")
	require.Contains(t, fetch.Get("parameters.properties.url.description").String(), "HTTP or HTTPS")
	require.Equal(t, "string", fetch.Get("parameters.properties.url.type").String())
	require.JSONEq(t, `["url"]`, fetch.Get("parameters.required").Raw)
	require.False(t, fetch.Get("parameters.additionalProperties").Bool())
	require.Len(t, fetch.Get("parameters.properties").Map(), 1)
	for _, choice := range []string{`"none"`, `"auto"`} {
		noPermission := []byte(`{"tools":[{"type":"function","name":"client_tool","parameters":{}}],"tool_choice":` + choice + `,"input":"read a page"}`)
		unmodified, changed, err := prepareDeepSeekHostedSearchRequest(noPermission)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, noPermission, unmodified, "fetch requires an explicit hosted web_search permission")
	}
	for _, collision := range []string{
		`{"type":"function","name":"` + deepSeekHostedFetchToolName + `"}`,
		`{"type":"function","function":{"name":"` + deepSeekHostedFetchToolName + `"}}`,
	} {
		conflict := []byte(`{"tools":[{"type":"web_search"},` + collision + `],"input":"hi"}`)
		unmodified, changed, err := prepareDeepSeekHostedSearchRequest(conflict)
		require.ErrorContains(t, err, "conflicts with a client tool")
		require.False(t, changed)
		require.Equal(t, conflict, unmodified)
	}
}
