package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	VideoProviderSeedance  = "seedance"
	VideoProviderMiniMaxH3 = "minimax-h3"
)

func (e GrokMediaEndpoint) VideoProvider() string {
	if e.IsSeedance() {
		return VideoProviderSeedance
	}
	if e.IsMiniMaxH3() {
		return VideoProviderMiniMaxH3
	}
	return ""
}

func VideoProviderPlatform(provider string) string {
	switch provider {
	case VideoProviderSeedance:
		return PlatformOpenAI
	case VideoProviderMiniMaxH3:
		return PlatformMiniMax
	default:
		return PlatformGrok
	}
}

func videoProviderAccountMatches(account *Account, provider string) bool {
	if account == nil {
		return false
	}
	switch provider {
	case VideoProviderSeedance:
		return account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance)
	case VideoProviderMiniMaxH3:
		return account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityMiniMaxH3)
	default:
		return account.Platform == PlatformGrok
	}
}

// Persist ownership and the immutable billing snapshot before publishing a task
// ID. Once a create was sent, transport/storage ambiguity must never retry a
// second create on another account.
func (s *OpenAIGatewayService) acceptVideoProviderTask(ctx context.Context, account *Account, result *OpenAIForwardResult) error {
	owner, ok := grokVideoBindOwnerFrom(ctx)
	if !ok {
		return nil
	}
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	rememberGrokVideoAccount(derefGroupID(owner.GroupID), owner.UserID, owner.APIKeyID, result.ResponseID, account, grokVideoPendingBillingTTL(s.cfg))
	_ = s.BindGrokMediaVideoRequestAccount(storeCtx, owner.GroupID, result.ResponseID, owner.UserID, owner.APIKeyID, account.ID)
	if owner.PendingBilling == nil {
		return nil
	}
	pending := *owner.PendingBilling
	pending.UpstreamModel = result.UpstreamModel
	if owner.OnAccepted != nil {
		if err := owner.OnAccepted(storeCtx, result.ResponseID, pending); err != nil {
			return ErrBillingServiceUnavailable.WithCause(err)
		}
	}
	if err := s.StoreGrokVideoPendingBilling(storeCtx, result.ResponseID, owner.UserID, owner.APIKeyID, pending); err != nil {
		return ErrBillingServiceUnavailable.WithCause(err)
	}
	return nil
}

// Preserve upstream status while keeping URLs, credentials and arbitrary HTML
// out of customer errors. Details remain attributable by the request ID.
func writeVideoProviderError(c *gin.Context, resp *http.Response) error {
	status := resp.StatusCode
	setOpsUpstreamError(c, status, "Video provider request failed", "")
	MarkResponseCommitted(c)
	c.JSON(status, gin.H{"error": gin.H{"type": "upstream_error", "message": "Video provider request failed"}})
	return fmt.Errorf("video provider upstream status %d", status)
}

func rejectVideoProviderCreate(ctx context.Context) error {
	if owner, ok := grokVideoBindOwnerFrom(ctx); ok && owner.OnRejected != nil {
		if err := owner.OnRejected(ctx); err != nil {
			return ErrBillingServiceUnavailable.WithCause(err)
		}
	}
	return nil
}

func videoProviderContentProxyURL(provider, taskID string) string {
	switch provider {
	case VideoProviderSeedance:
		return "/api/v3/contents/generations/tasks/" + url.PathEscape(strings.TrimPrefix(taskID, "seedance:")) + "/content"
	default:
		return "/v1/videos/" + url.PathEscape(strings.TrimPrefix(taskID, "minimax-h3:")) + "/content"
	}
}

// Download signed media without upstream credentials or account header
// overrides. The shared HTTP transport validates every resolved public host,
// including redirects, so a provider cannot turn its output URL into SSRF.
func (s *OpenAIGatewayService) forwardVideoProviderContent(ctx context.Context, c *gin.Context, account *Account, rawURL string, result *OpenAIForwardResult) (*OpenAIForwardResult, error) {
	if !validMiniMaxH3OutputURL(rawURL) {
		return nil, fmt.Errorf("video provider returned an invalid content URL")
	}
	req, err := http.NewRequestWithContext(WithHTTPUpstreamPublicHostsOnly(ctx), http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid video content request")
	}
	req.Header.Set("Accept", "video/*,application/octet-stream")
	if value := strings.TrimSpace(c.GetHeader("Range")); value != "" {
		req.Header.Set("Range", value)
	}
	proxy := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
	if err != nil {
		return nil, fmt.Errorf("video content download failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		return nil, writeVideoProviderError(c, resp)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable && !strings.HasPrefix(mediaType, "video/") && mediaType != "application/octet-stream" {
		return nil, fmt.Errorf("video content response is not video data")
	}
	if err := runGrokMediaBeforePublish(ctx, result); err != nil {
		return nil, err
	}
	c.Header("Cache-Control", "private, no-store")
	if err := writeGrokMediaContentResponse(c, resp); err != nil {
		return nil, err
	}
	return result, nil
}

func parseSeedanceCompletion(body []byte, result *OpenAIForwardResult) error {
	result.VideoStatus = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "status").String()))
	if result.VideoStatus != "succeeded" {
		return nil
	}
	tokens := gjson.GetBytes(body, "usage.completion_tokens")
	if tokens.Type != gjson.Number || tokens.Float() < 0 || tokens.Float() != float64(tokens.Int()) || tokens.Float() > float64(int(^uint(0)>>1)) {
		return fmt.Errorf("completed Seedance task has no valid completion usage")
	}
	result.VideoCount, result.VideoTokenBilling = 1, true
	result.Usage.OutputTokens = int(tokens.Int())
	result.VideoDurationSeconds = int(gjson.GetBytes(body, "duration").Int())
	return nil
}
