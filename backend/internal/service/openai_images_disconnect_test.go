//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Owner decision 2026-11-06: when the client leaves, the upstream connection is
// closed at once, on the image endpoints too. Images are billed per image or per
// image token, so nothing is estimated here: what the upstream already reported
// and the images it already completed are billed, and a request that reported
// and completed nothing is not billed. These tests drive the real ForwardImages
// for the three upstream shapes (API key, OAuth Responses bridge, OAuth native
// Codex images) against an upstream whose body the test controls.

const (
	imagesAPIKeyPartialFrame   = "data: {\"type\":\"image_generation.partial_image\",\"b64_json\":\"cGFydGlhbA==\"}\n\n"
	imagesAPIKeyCompletedFrame = "data: {\"type\":\"image_generation.completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"output_tokens_details\":{\"image_tokens\":2}},\"b64_json\":\"ZmluYWw=\",\"output_format\":\"png\"}\n\n"
)

type imagesDisconnectKind struct {
	name    string
	account func() *Account
	// streamBody and bufferedBody are the client requests.
	streamBody   string
	bufferedBody string
	// partialFrame is one upstream stream frame that carries only a partial image.
	partialFrame string
	// bufferedStart is how the upstream answer to the buffered request begins.
	bufferedStart string
	bufferedType  string
}

var imagesDisconnectKinds = []imagesDisconnectKind{
	{
		name: "api_key",
		account: func() *Account {
			return &Account{ID: 8, Name: "openai-apikey", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-api-key"}}
		},
		streamBody:    `{"model":"gpt-image-2","prompt":"draw a cat","stream":true}`,
		bufferedBody:  `{"model":"gpt-image-2","prompt":"draw a cat"}`,
		partialFrame:  imagesAPIKeyPartialFrame,
		bufferedStart: `{"created":1710000000,"data":[{"b64_json":"`,
		bufferedType:  "application/json",
	},
	{
		name: "oauth_bridge",
		account: func() *Account {
			return &Account{ID: 9, Name: "openai-oauth", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "token-123"}}
		},
		streamBody:    `{"model":"gpt-image-1","prompt":"draw a cat","stream":true,"response_format":"url"}`,
		bufferedBody:  `{"model":"gpt-image-1","prompt":"draw a cat","response_format":"b64_json"}`,
		partialFrame:  "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydGlhbA==\",\"partial_image_index\":0,\"output_format\":\"png\"}\n\n",
		bufferedStart: "data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000000}}\n\n",
		bufferedType:  "text/event-stream",
	},
	{
		name:          "oauth_direct",
		account:       newDirectImagesOAuthAccount,
		streamBody:    `{"model":"gpt-image-2.6-flare","prompt":"draw a cat","stream":true,"response_format":"url"}`,
		bufferedBody:  `{"model":"gpt-image-2.6-flare","prompt":"draw a cat","response_format":"b64_json"}`,
		partialFrame:  "event: image_generation.partial_image\ndata: {\"type\":\"image_generation.partial_image\",\"partial_image_index\":0,\"b64_json\":\"cGFydGlhbA==\",\"output_format\":\"png\"}\n\n",
		bufferedStart: `{"created":1710000000,"data":[{"b64_json":"`,
		bufferedType:  "application/json",
	},
}

// imagesDisconnectIntervals covers the two stream readers: 0 reads the upstream
// inline, a positive interval reads it in a goroutine with an idle watchdog.
var imagesDisconnectIntervals = []int{0, 30}

func newImagesDisconnectService(upstream HTTPUpstream, streamIntervalSeconds int) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg: &config.Config{
			Gateway:  config.GatewayConfig{ImageStreamDataIntervalTimeout: streamIntervalSeconds},
			Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}},
		},
	}
}

func newImagesDisconnectRequest(t *testing.T, ctx context.Context, body string) (*gin.Context, *openAIResponseFlushRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := newOpenAIResponseFlushRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader([]byte(body))).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

// startImagesDisconnectForward starts ForwardImages against an upstream whose
// body is a pipe the test writes to.
func startImagesDisconnectForward(t *testing.T, kind imagesDisconnectKind, streaming bool, streamIntervalSeconds int) *disconnectPipeHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	body, contentType := kind.bufferedBody, kind.bufferedType
	if streaming {
		body, contentType = kind.streamBody, "text/event-stream"
	}
	c, recorder := newImagesDisconnectRequest(t, ctx, body)
	resp, tracked, pw := newLifecycleTrackingResponse()
	resp.Header = http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"req_img_disconnect"}}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = tracked.Close()
	})
	upstream := &httpUpstreamRecorder{resp: resp}
	svc := newImagesDisconnectService(upstream, streamIntervalSeconds)
	parsed, err := svc.ParseOpenAIImagesRequest(c, []byte(body))
	require.NoError(t, err)
	account := kind.account()

	h := &disconnectPipeHarness{
		t: t, ctx: ctx, cancel: cancel, recorder: recorder, upstream: upstream, pipeW: pw, tracked: tracked,
		done: make(chan disconnectOutcome, 1),
	}
	done := h.done
	go func() {
		result, err := svc.ForwardImages(ctx, c, account, []byte(body), parsed, "")
		done <- disconnectOutcome{result: result, err: err}
	}()
	return h
}

// billImagesDisconnectResult plays the handler's usage step for an image result:
// a nil result is a no-op, anything else goes through RecordUsage with a group
// that prices every image at the same amount.
func billImagesDisconnectResult(t *testing.T, result *OpenAIForwardResult) (*openAIRecordUsageLogRepoStub, *openAIRecordUsageBillingRepoStub) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	if result == nil {
		return usageRepo, billingRepo
	}
	price := 0.04
	groupID := int64(77)
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil,
	)
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: result,
		APIKey: &APIKey{ID: 1000, GroupID: i64p(groupID), Group: &Group{
			ID: groupID, RateMultiplier: 1, ImagePrice1K: &price, ImagePrice2K: &price, ImagePrice4K: &price,
		}},
		User:    &User{ID: 2000},
		Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	return usageRepo, billingRepo
}

// requireNothingBilled asserts the outcome carries no billable work: no result,
// a plain error that is not a failover, and no charge.
func requireImagesNothingBilled(t *testing.T, out disconnectOutcome) {
	t.Helper()
	t.Logf("ForwardImages returned: result_nil=%t err=%v", out.result == nil, out.err)
	require.Error(t, out.err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(out.err, &failoverErr), "a departed client must not fail over to another account")
	require.Nil(t, out.result, "nothing was reported or completed, so nothing may be billed")
	usageRepo, billingRepo := billImagesDisconnectResult(t, out.result)
	require.Zero(t, usageRepo.calls)
	require.Nil(t, billingRepo.lastCmd)
}

// The client leaves after one image was completed and reported while the upstream
// keeps the connection open. The upstream is closed at once and exactly that image
// is billed with the usage the upstream reported. The old code kept reading a body
// nobody closed, so ForwardImages never returned.
func TestOpenAIImagesStream_ClientLeavesAfterCompletedImageIsBilledAndUpstreamClosed(t *testing.T) {
	for _, interval := range imagesDisconnectIntervals {
		t.Run(fmt.Sprintf("interval_%d", interval), func(t *testing.T) {
			h := startImagesDisconnectForward(t, imagesDisconnectKinds[0], true, interval)
			h.write(imagesAPIKeyPartialFrame, imagesAPIKeyCompletedFrame)
			waitOpenAIResponseFlushCount(t, h.recorder, 4)
			canceledAt := time.Now()
			h.cancel()

			out := h.await(3 * time.Second)

			require.Less(t, time.Since(canceledAt), 2*time.Second)
			require.Error(t, out.err)
			require.Len(t, h.upstream.requests, 1, "a canceled client must not trigger a retry")
			h.requireUpstreamClosed()
			require.NotNil(t, out.result, "an image the upstream completed and reported must reach billing")
			require.Equal(t, 1, out.result.ImageCount, "the image count is what the upstream completed, never invented")
			require.Equal(t, 3, out.result.Usage.InputTokens)
			require.Equal(t, 4, out.result.Usage.OutputTokens)
			require.Equal(t, 2, out.result.Usage.ImageOutputTokens)
			usageRepo, billingRepo := billImagesDisconnectResult(t, out.result)
			require.Equal(t, 1, usageRepo.calls)
			require.NotNil(t, billingRepo.lastCmd)
			require.InDelta(t, 0.04, billingRepo.lastCmd.BalanceCost, 1e-9, "exactly one image is charged")
		})
	}
}

// The client leaves with only a partial image delivered. Nothing was completed or
// reported, so nothing is billed and nothing is invented; the upstream is closed.
func TestOpenAIImagesStream_ClientLeavesBeforeAnyCompletedImageIsNotBilled(t *testing.T) {
	for _, kind := range imagesDisconnectKinds {
		for _, interval := range imagesDisconnectIntervals {
			t.Run(fmt.Sprintf("%s/interval_%d", kind.name, interval), func(t *testing.T) {
				h := startImagesDisconnectForward(t, kind, true, interval)
				h.write(kind.partialFrame)
				canceledAt := time.Now()
				h.cancel()

				out := h.await(3 * time.Second)

				require.Less(t, time.Since(canceledAt), 2*time.Second)
				h.requireUpstreamClosed()
				requireImagesNothingBilled(t, out)
			})
		}
	}
}

// The client vanishes without the request context ending: every write to it fails.
// The old code only logged "continue draining" and read a quiet upstream forever.
func TestOpenAIImagesStream_ClientWriteFailureClosesUpstream(t *testing.T) {
	for _, kind := range imagesDisconnectKinds {
		for _, interval := range imagesDisconnectIntervals {
			t.Run(fmt.Sprintf("%s/interval_%d", kind.name, interval), func(t *testing.T) {
				h := startImagesDisconnectForward(t, kind, true, interval)
				h.failClientWrites()
				h.write(kind.partialFrame)

				out := h.await(3 * time.Second)

				h.requireUpstreamClosed()
				requireImagesNothingBilled(t, out)
			})
		}
	}
}

// The client leaves while a buffered (non-stream) answer is still arriving. The
// upstream is closed at once; no usage was reported and no image completed, so
// nothing is billed. The native Codex path must also not read the self-inflicted
// close as an upstream fault and fail over.
func TestOpenAIImagesBuffered_ClientLeavesWhileBodyArrivesClosesUpstream(t *testing.T) {
	for _, kind := range imagesDisconnectKinds {
		t.Run(kind.name, func(t *testing.T) {
			h := startImagesDisconnectForward(t, kind, false, 0)
			h.write(kind.bufferedStart)
			canceledAt := time.Now()
			h.cancel()

			out := h.await(3 * time.Second)

			require.Less(t, time.Since(canceledAt), 2*time.Second)
			h.requireUpstreamClosed()
			requireImagesNothingBilled(t, out)
		})
	}
}

// The client leaves while the upstream is still working on the response headers.
// The old code ran the upstream call detached from the client; now it is canceled
// at once and the departed client does not fail over.
func TestOpenAIImages_ClientLeavesBeforeResponseHeadersCancelsUpstream(t *testing.T) {
	for _, kind := range imagesDisconnectKinds {
		for _, streaming := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/stream_%t", kind.name, streaming), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				body := kind.bufferedBody
				if streaming {
					body = kind.streamBody
				}
				c, _ := newImagesDisconnectRequest(t, ctx, body)
				upstream := newPassthroughBlockingUpstream()
				t.Cleanup(upstream.unblock)
				svc := newImagesDisconnectService(upstream, 0)
				parsed, err := svc.ParseOpenAIImagesRequest(c, []byte(body))
				require.NoError(t, err)
				account := kind.account()

				done := make(chan disconnectOutcome, 1)
				go func() {
					result, err := svc.ForwardImages(ctx, c, account, []byte(body), parsed, "")
					done <- disconnectOutcome{result: result, err: err}
				}()
				select {
				case <-upstream.started:
				case <-time.After(3 * time.Second):
					t.Fatal("the request never reached the upstream")
				}
				canceledAt := time.Now()
				cancel()

				select {
				case out := <-done:
					require.Less(t, time.Since(canceledAt), 2*time.Second)
					require.ErrorIs(t, out.err, context.Canceled)
					require.EqualValues(t, 1, upstream.calls.Load())
					requireImagesNothingBilled(t, out)
					// The upstream did not fail; the client left. No upstream error event
					// is recorded for it (it would count against the account).
					events, _ := c.Get(OpsUpstreamErrorsKey)
					require.Empty(t, events, "a departed client must not be recorded as an upstream error")
				case <-time.After(3 * time.Second):
					t.Fatal("the upstream request outlived the departed client")
				}
			})
		}
	}
}
