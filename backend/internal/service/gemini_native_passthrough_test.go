package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const nativeGeminiSearchRequest = `{"contents":[{"role":"user","parts":[{"text":"Search for an official source."}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"q":"example"}},"thoughtSignature":"signed-history"}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"ok":true}}}]}],"systemInstruction":{"parts":[{"text":"Cite sources."}]},"tools":[{"googleSearch":{}},{"functionDeclarations":[{"name":"lookup","parameters":{"type":"OBJECT","properties":{"q":{"type":"STRING"}}}}]}],"generationConfig":{"maxOutputTokens":128,"thinkingConfig":{"includeThoughts":true}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_ONLY_HIGH"}],"cachedContent":"cachedContents/fixture"}`

const nativeGeminiGroundedResponse = `{"candidates":[{"content":{"role":"model","parts":[{"text":"Answer with a source.","thoughtSignature":"signed-answer"}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["official example"],"groundingChunks":[{"web":{"uri":"https://example.com/source","title":"Official source"}}],"groundingSupports":[{"segment":{"startIndex":0,"endIndex":6},"groundingChunkIndices":[0]}],"searchEntryPoint":{"renderedContent":"<div>sources</div>"}}}],"usageMetadata":{"promptTokenCount":30,"cachedContentTokenCount":10,"candidatesTokenCount":6,"thoughtsTokenCount":2,"totalTokenCount":38},"modelVersion":"gemini-native-fixture","responseId":"native-response"}`

func TestGeminiNativeAdaptivePreservesGoogleSearchAndGroundedResponses(t *testing.T) {
	for _, protocol := range []string{"", APIProtocolAdaptive} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/json"
			if stream {
				name = protocol + "/sse"
			}
			t.Run(name, func(t *testing.T) {
				account := geminiBridgeAccount(protocol)
				account.Credentials["base_url"] = "https://upstream.example.com/relay/v1"
				account.Credentials["api_base_urls"] = map[string]any{APIProtocolResponses: "https://responses.example.com/v1"}
				action := "generateContent"
				response := nativeGeminiGroundedResponse
				contentType := "application/json"
				if stream {
					action = "streamGenerateContent"
					response = "data: " + nativeGeminiGroundedResponse + "\n\n"
					contentType = "text/event-stream"
				}
				calls := 0
				svc := &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: &geminiBridgeUpstream{do: func(req *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, "/relay/v1beta/models/gemini-4-argon:"+action, req.URL.Path)
					require.Equal(t, stream, req.URL.Query().Get("alt") == "sse")
					require.Equal(t, "local-fake-key", req.Header.Get("x-goog-api-key"))
					require.Empty(t, req.Header.Get("Authorization"))
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.JSONEq(t, nativeGeminiSearchRequest, string(body))
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}, "X-Request-Id": {"native-upstream"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				}}}
				c, rec := geminiBridgeContext()
				result, err := svc.ForwardNative(context.Background(), c, account, "gemini-4-argon", action, stream, []byte(nativeGeminiSearchRequest))
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, response, rec.Body.String())
				require.Equal(t, "native-upstream", result.RequestID)
				require.Equal(t, stream, result.Stream)
				require.Equal(t, 20, result.Usage.InputTokens)
				require.Equal(t, 10, result.Usage.CacheReadInputTokens)
				require.Equal(t, 8, result.Usage.OutputTokens)
				require.Equal(t, "/v1beta/models", GetActualOpenAIUpstreamEndpoint(c))
			})
		}
	}
}

func TestGeminiNativeAdaptiveErrorDoesNotConvertOrRetry(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			body := `{"error":{"code":` + strconv.Itoa(status) + `,"message":"native failure","status":"FAILED_PRECONDITION"}}`
			calls := 0
			svc := &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: &geminiBridgeUpstream{do: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Contains(t, req.URL.Path, "/v1beta/models/")
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}}}
			c, rec := geminiBridgeContext()
			result, err := svc.ForwardNative(context.Background(), c, geminiBridgeAccount(APIProtocolAdaptive), "gemini-4-argon", "generateContent", false, []byte(nativeGeminiSearchRequest))
			require.Error(t, err)
			require.Nil(t, result)
			if status == http.StatusForbidden {
				var failover *UpstreamFailoverError
				require.True(t, errors.As(err, &failover))
				require.Equal(t, status, failover.StatusCode)
				require.JSONEq(t, body, string(failover.ResponseBody))
			} else {
				require.Equal(t, status, rec.Code)
				require.JSONEq(t, body, rec.Body.String())
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestGeminiNativeModelsUsesNativeBaseURL(t *testing.T) {
	account := geminiBridgeAccount(APIProtocolAdaptive)
	account.Credentials["api_base_urls"] = map[string]any{"gemini": "https://native.example.com/prefix/v1beta/", "responses": "https://responses.example.com/v1"}
	body := `{"models":[{"name":"models/gemini-4-argon","supportedGenerationMethods":["generateContent","streamGenerateContent","countTokens"],"customField":42}],"nextPageToken":"next"}`
	svc := &GeminiMessagesCompatService{cfg: &config.Config{}, httpUpstream: &geminiBridgeUpstream{do: func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "native.example.com", req.URL.Host)
		require.Equal(t, "/prefix/v1beta/models", req.URL.Path)
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "local-fake-key", req.Header.Get("x-goog-api-key"))
		return geminiBridgeResponse(body), nil
	}}}
	result, err := svc.ForwardAIStudioGET(context.Background(), account, "/v1beta/models")
	require.NoError(t, err)
	require.Equal(t, body, string(result.Body))
	require.Equal(t, "next", gjson.GetBytes(result.Body, "nextPageToken").String())
}
