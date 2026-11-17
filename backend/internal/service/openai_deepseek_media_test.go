package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deepSeekMediaHTTPClient struct {
	client *http.Client
}

func (u *deepSeekMediaHTTPClient) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}

func (u *deepSeekMediaHTTPClient) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func deepSeekMediaFixture(server *httptest.Server) (*OpenAIGatewayService, *Account) {
	return &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &deepSeekMediaHTTPClient{client: server.Client()}},
		&Account{ID: 7, Type: AccountTypeAPIKey, Platform: PlatformDeepseek,
			Credentials: map[string]any{"api_key": "local-fixture", "base_url": server.URL + "/v1/"}}
}

func deepSeekMediaContext(path, contentType string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	return c, recorder
}

func TestDeepSeekTTSBillableCharacters(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{
		{"", 0}, {"你好", 4}, {"DeepSeek", 8}, {"你好，我是 DeepSeek。", 19},
		{"， 。 ", 4}, {"a b", 3}, {"\U0001f600", 1}, {"かな", 2}, {"\U00020000", 2},
	} {
		t.Run(tc.text, func(t *testing.T) {
			require.Equal(t, tc.want, DeepSeekTTSBillableCharacters(tc.text))
		})
	}
}

func TestDeepSeekMediaPassThroughAndFixedBilling(t *testing.T) {
	for _, tc := range []struct {
		path, label, contentType, body, responseType, response string
		cost                                                   float64
	}{
		{DeepSeekToolsPathASR, DeepSeekToolsLabelASR, `multipart/form-data; boundary="native-boundary"`, "--native-boundary\r\nraw audio\x00\xff", "application/json", `{"request_id":"asr-local","text":"recognized","duration_ms":3200}`, 4.0 / 3600 * 0.78},
		{DeepSeekToolsPathTTS, DeepSeekToolsLabelTTS, "application/json", `{"text":"你好，我是 DeepSeek。","voice_id":"tide","format":"pcm","filter_markdown":false}`, "audio/pcm", "\x00\x01\x02\xff", 0.0057},
		{DeepSeekToolsPathOCR, DeepSeekToolsLabelOCR, "multipart/form-data; boundary=original-boundary", "--original-boundary\r\nraw file\x00", "application/json", "\n\n" + `{"id":"ocr-local","object":"ocr.result","page_count":2,"pages":[]}`, 0.003},
	} {
		t.Run(tc.label, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, "Bearer local-fixture", r.Header.Get("Authorization"))
				assert.Equal(t, tc.contentType, r.Header.Get("Content-Type"))
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Equal(t, []byte(tc.body), body)
				w.Header().Set("Content-Type", tc.responseType)
				w.Header().Set("x-request-id", "native-local")
				_, err = io.WriteString(w, tc.response)
				assert.NoError(t, err)
			}))
			defer server.Close()
			svc, account := deepSeekMediaFixture(server)
			c, recorder := deepSeekMediaContext(tc.path, tc.contentType, []byte(tc.body))
			result, err := svc.ForwardDeepSeekMedia(t.Context(), c, account, tc.path, tc.label, []byte(tc.body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.label, result.Model)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, tc.responseType, recorder.Header().Get("Content-Type"))
			require.Equal(t, tc.response, recorder.Body.String())
			require.Equal(t, "native-local", recorder.Header().Get("x-request-id"))
			require.True(t, result.FixedToolBilling)
			switch tc.path {
			case DeepSeekToolsPathOCR:
				require.Equal(t, 2, result.OCRPages)
			case DeepSeekToolsPathASR:
				require.Equal(t, 4.0/3600, result.AudioUsage.DurationOrUnits)
			}
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			bills := &openAIRecordUsageBillingRepoStub{}
			billingSvc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, bills, nil, nil, nil)
			group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: 0.3}
			key := &APIKey{ID: 1, GroupID: &group.ID, Group: group, User: &User{ID: 2}}
			// A Search-only provider setting must never replace media credentials.
			searchURL, searchCredential := "https://search.invalid", "unused-local-fixture"
			group.SearchProviderBaseURL, group.SearchProviderAPIKey = &searchURL, &searchCredential
			rawCost, err := billingSvc.calculateOpenAIRecordUsageCost(t.Context(), result, key, []string{tc.label}, .3, .3, .3, .3, UsageTokens{}, "", nil, time.Now())
			require.NoError(t, err)
			require.InDelta(t, tc.cost, rawCost.ActualCost, 1e-12)
			require.NoError(t, billingSvc.RecordUsage(t.Context(), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account}))
			require.Equal(t, QuantizeUsageBillingAmount(tc.cost), logs.lastLog.ActualCost)
			require.Equal(t, 1.0, logs.lastLog.RateMultiplier)
			require.Equal(t, QuantizeUsageBillingAmount(tc.cost), bills.lastCmd.BalanceCost)

			for _, override := range []float64{0, 2} {
				group.OCRPricePer1kPages, group.AudioSTTPricePerHour, group.AudioTTSPricePerMillionChars = &override, &override, &override
				cost, err := billingSvc.calculateOpenAIRecordUsageCost(t.Context(), result, key, []string{tc.label}, .3, .3, .3, .3, UsageTokens{}, "", nil, time.Now())
				require.NoError(t, err)
				units := 2.0 / 1000
				if result.AudioUsage != nil {
					units = result.AudioUsage.DurationOrUnits
				}
				require.InDelta(t, units*override, cost.ActualCost, 1e-12)
			}
		})
	}
}

func TestDeepSeekMediaClientErrorRelayFingerprint(t *testing.T) {
	const response = `{"error":{"message":"voice_id must be stella or tide (request id: 20260101000000000000000000aaaaaaSyntheticA1)","type":"new_api_error","param":"","code":"invalid_request"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, err := io.WriteString(w, response)
		assert.NoError(t, err)
	}))
	defer server.Close()
	svc, account := deepSeekMediaFixture(server)
	svc.cfg.Gateway.FailoverOn400 = true
	c, recorder := deepSeekMediaContext(DeepSeekToolsPathTTS, "application/json", nil)
	result, err := svc.ForwardDeepSeekMedia(t.Context(), c, account, DeepSeekToolsPathTTS, DeepSeekToolsLabelTTS, nil)
	require.NoError(t, err)
	require.Nil(t, result, "validation failures must not bill")
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "new_api_error")
	require.NotContains(t, recorder.Body.String(), "request id:")
	require.Equal(t, response, c.GetString(OpsUpstreamErrorDetailKey))
	require.Contains(t, c.GetString(OpsUpstreamErrorMessageKey), "request id:")
	require.JSONEq(t, `{"error":{"message":"voice_id must be stella or tide","type":"invalid_request_error","param":"","code":"invalid_request"}}`, recorder.Body.String())
}

func TestDeepSeekMediaSuccessfulNon200KeepsOpsClear(t *testing.T) {
	const response = `{"id":"accepted"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, err := io.WriteString(w, response)
		assert.NoError(t, err)
	}))
	defer server.Close()
	svc, account := deepSeekMediaFixture(server)
	c, recorder := deepSeekMediaContext(DeepSeekToolsPathASR, "application/json", nil)
	result, err := svc.ForwardDeepSeekMedia(t.Context(), c, account, DeepSeekToolsPathASR, DeepSeekToolsLabelASR, nil)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, response, recorder.Body.String())
	require.Empty(t, c.GetString(OpsUpstreamErrorMessageKey))
	require.Empty(t, c.GetString(OpsUpstreamErrorDetailKey))
	require.Zero(t, c.GetInt(OpsUpstreamStatusCodeKey))
}

func TestDeepSeekMediaStreamingFlushBeforeUpstreamFinishes(t *testing.T) {
	for _, path := range []string{DeepSeekToolsPathTTS, DeepSeekToolsPathOCR} {
		t.Run(path, func(t *testing.T) {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/wav")
				first, last := "RIFF", "audio"
				if path == DeepSeekToolsPathOCR {
					w.Header().Set("Content-Type", "application/json")
					first, last = "\n\n", `{"id":"ocr-stream","page_count":2,"pages":[]}`
				}
				if _, err := io.WriteString(w, first); !assert.NoError(t, err) {
					return
				}
				flusher, ok := w.(http.Flusher)
				if !assert.True(t, ok) {
					return
				}
				flusher.Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, err := io.WriteString(w, last)
				assert.NoError(t, err)
			}))
			t.Cleanup(upstream.Close)
			t.Cleanup(upstream.CloseClientConnections)
			svc, account := deepSeekMediaFixture(upstream)
			results := make(chan *OpenAIForwardResult, 1)
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, _ := gin.CreateTestContext(w)
				c.Request = r
				result, err := svc.ForwardDeepSeekMedia(r.Context(), c, account, path, DeepSeekToolsLabelTTS, []byte(`{"text":"hello"}`))
				assert.NoError(t, err)
				results <- result
			}))
			t.Cleanup(gateway.Close)
			t.Cleanup(gateway.CloseClientConnections)
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Get(gateway.URL)
			require.NoError(t, err)
			defer func() { require.NoError(t, resp.Body.Close()) }()
			first := make([]byte, 4)
			if path == DeepSeekToolsPathOCR {
				first = make([]byte, 2)
			}
			_, err = io.ReadFull(resp.Body, first)
			require.NoError(t, err)
			select {
			case <-results:
				t.Fatal("response was buffered until upstream completion")
			default:
			}
			release <- struct{}{}
			remaining, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			result := <-results
			require.NotNil(t, result)
			if path == DeepSeekToolsPathOCR {
				require.True(t, json.Valid(append(first, remaining...)))
				require.Equal(t, 2, result.OCRPages)
			} else {
				require.Equal(t, "RIFFaudio", string(append(first, remaining...)))
			}
		})
	}
}

func TestDeepSeekMediaDoesNotBillFailures(t *testing.T) {
	for _, tc := range []struct {
		path, response string
		status         int
	}{
		{DeepSeekToolsPathASR, `{"text":"missing duration"}`, 200},
		{DeepSeekToolsPathASR, `{"duration_ms":3200}`, 200},
		{DeepSeekToolsPathASR, `{"text":"","duration_ms":0}`, 200},
		{DeepSeekToolsPathOCR, "\n" + `{"error":{"message":"failed","code":"timeout"},"page_count":9}`, 200},
		{DeepSeekToolsPathOCR, `{"page_count":0}`, 200},
		{DeepSeekToolsPathOCR, `{"page_count":2`, 200},
		{DeepSeekToolsPathTTS, `{"error":{"message":"Content Exists Risk"}}`, 400},
		{DeepSeekToolsPathASR, `{"error":{"message":"unavailable"}}`, 503},
		{DeepSeekToolsPathOCR, `{"error":{"message":"bad file"}}`, 400},
	} {
		t.Run(tc.path+tc.response, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, err := io.WriteString(w, tc.response)
				assert.NoError(t, err)
			}))
			defer server.Close()
			svc, account := deepSeekMediaFixture(server)
			svc.cfg.Gateway.FailoverOn400 = true
			c, recorder := deepSeekMediaContext(tc.path, "application/json", nil)
			result, err := svc.ForwardDeepSeekMedia(t.Context(), c, account, tc.path, "failure", nil)
			require.Nil(t, result)
			if tc.status == 503 {
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.False(t, recorder.Flushed)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.status, recorder.Code)
			}
		})
	}
}

func TestDeepSeekTTSInterruptedUpstreamIsNotBilled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Content-Length", "100")
		_, err := io.WriteString(w, "short")
		assert.NoError(t, err)
	}))
	defer server.Close()
	svc, account := deepSeekMediaFixture(server)
	c, _ := deepSeekMediaContext(DeepSeekToolsPathTTS, "application/json", nil)
	result, err := svc.ForwardDeepSeekMedia(t.Context(), c, account, DeepSeekToolsPathTTS, DeepSeekToolsLabelTTS, []byte(`{"text":"hello"}`))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Nil(t, result)
}

func TestDeepSeekRecognitionSurvivesClientCancellation(t *testing.T) {
	for _, path := range []string{DeepSeekToolsPathASR, DeepSeekToolsPathOCR} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cancel()
				assert.NoError(t, r.Context().Err(), "client cancellation must not reach recognition")
				payload := `{"text":"recognized","duration_ms":3200}`
				if path == DeepSeekToolsPathOCR {
					payload = "\n\n" + `{"id":"ocr-cancel","page_count":2}`
				}
				_, err := io.WriteString(w, payload)
				assert.NoError(t, err)
			}))
			defer server.Close()
			svc, account := deepSeekMediaFixture(server)
			c, _ := deepSeekMediaContext(path, "multipart/form-data; boundary=native", []byte("native file"))
			c.Request = c.Request.WithContext(ctx)
			label := DeepSeekToolsLabelASR
			if path == DeepSeekToolsPathOCR {
				label = DeepSeekToolsLabelOCR
			}
			result, err := svc.ForwardDeepSeekMedia(ctx, c, account, path, label, []byte("native file"))
			require.NoError(t, err)
			require.NotNil(t, result, "completed recognition remains billable")
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			bills := &openAIRecordUsageBillingRepoStub{}
			billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, bills, nil, nil, nil)
			group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: .3}
			key := &APIKey{ID: 1, GroupID: &group.ID, Group: group, User: &User{ID: 2}}
			require.NoError(t, billing.RecordUsage(context.WithoutCancel(ctx), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account}))
			want := 4.0 / 3600 * .78
			if path == DeepSeekToolsPathOCR {
				require.Equal(t, 2, result.OCRPages)
				want = .003
			} else {
				require.Equal(t, 4.0/3600, result.AudioUsage.DurationOrUnits)
			}
			require.Equal(t, QuantizeUsageBillingAmount(want), logs.lastLog.ActualCost)
			require.Equal(t, QuantizeUsageBillingAmount(want), bills.lastCmd.BalanceCost)
		})
	}
}

type deepSeekDisconnectWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *deepSeekDisconnectWriter) Write(body []byte) (int, error) {
	w.cancel()
	return 0, errors.New("client disconnected")
}

func TestDeepSeekTTSClientDisconnectAfterAudioStartedBillsFullText(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.WriteString(w, "audio")
		assert.NoError(t, err)
		flusher, ok := w.(http.Flusher)
		if !assert.True(t, ok) {
			return
		}
		flusher.Flush()
		<-r.Context().Done()
		close(upstreamCanceled)
	}))
	defer server.Close()
	svc, account := deepSeekMediaFixture(server)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := &deepSeekDisconnectWriter{httptest.NewRecorder(), cancel}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, DeepSeekToolsPathTTS, nil).WithContext(ctx)
	result, err := svc.ForwardDeepSeekMedia(ctx, c, account, DeepSeekToolsPathTTS, DeepSeekToolsLabelTTS, []byte(`{"text":"你好，我是 DeepSeek。"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 19.0/1_000_000, result.AudioUsage.DurationOrUnits)
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	bills := &openAIRecordUsageBillingRepoStub{}
	billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, bills, nil, nil, nil)
	group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: .3}
	key := &APIKey{ID: 1, GroupID: &group.ID, Group: group, User: &User{ID: 2}}
	require.NoError(t, billing.RecordUsage(context.WithoutCancel(ctx), &OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account}))
	require.Equal(t, .0057, logs.lastLog.ActualCost)
	require.Equal(t, .0057, bills.lastCmd.BalanceCost)
	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("TTS upstream was not canceled")
	}
}

func TestDeepSeekOCRCaptureCeilingUsesCompletePrefixPageCount(t *testing.T) {
	for _, tc := range []struct {
		body         string
		limit, pages int
	}{
		{`{"page_count":2,"pages":["` + strings.Repeat("x", 200) + `"]}`, 64, 2},
		{`{"pages":["` + strings.Repeat("x", 200) + `"],"page_count":2}`, 64, 0},
		{`{"page_count":200,"pages":[]}`, 15, 0},
		{`{"error":{"message":"failed"},"page_count":2}`, 40, 0},
	} {
		t.Run(tc.body[:12], func(t *testing.T) {
			c, recorder := deepSeekMediaContext(DeepSeekToolsPathOCR, "", nil)
			pages, _, err := streamDeepSeekOCR(c, strings.NewReader(tc.body), tc.limit)
			require.NoError(t, err)
			require.Equal(t, tc.pages, pages)
			if strings.Contains(tc.body, `"pages"`) {
				require.Equal(t, tc.body, recorder.Body.String(), "the capture ceiling must not truncate the response")
			}
		})
	}
}
