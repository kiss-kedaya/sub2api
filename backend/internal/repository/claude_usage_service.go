package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const defaultClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

type claudeUsageService struct {
	usageURL string
}

func NewClaudeUsageFetcher() service.ClaudeUsageFetcher {
	return &claudeUsageService{usageURL: defaultClaudeUsageURL}
}

func (s *claudeUsageService) FetchUsage(ctx context.Context, accessToken, proxyURL string) (*service.ClaudeUsageResponse, error) {
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL: proxyURL,
		Timeout:  30 * time.Second,
	})
	if err != nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", s.usageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var usageResp service.ClaudeUsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usageResp); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &usageResp, nil
}
