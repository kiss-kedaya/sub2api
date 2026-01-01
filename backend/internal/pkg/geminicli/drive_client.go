package geminicli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// DriveStorageInfo represents Google Drive storage quota information
type DriveStorageInfo struct {
	Limit int64 `json:"limit"` // Storage limit in bytes
	Usage int64 `json:"usage"` // Current usage in bytes
}

// DriveClient interface for Google Drive API operations
type DriveClient interface {
	GetStorageQuota(ctx context.Context, accessToken, proxyURL string) (*DriveStorageInfo, error)
}

type driveClient struct {
	httpClient *http.Client
}

// NewDriveClient creates a new Drive API client
func NewDriveClient() DriveClient {
	return &driveClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GetStorageQuota fetches storage quota from Google Drive API
func (c *driveClient) GetStorageQuota(ctx context.Context, accessToken, proxyURL string) (*DriveStorageInfo, error) {
	const driveAPIURL = "https://www.googleapis.com/drive/v3/about?fields=storageQuota"

	req, err := http.NewRequestWithContext(ctx, "GET", driveAPIURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	// Get HTTP client with proxy support
	client, err := httpclient.GetClient(httpclient.Options{
		ProxyURL: proxyURL,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Retry logic with exponential backoff for rate limits
	var resp *http.Response
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to execute request: %w", err)
		}

		// Success
		if resp.StatusCode == http.StatusOK {
			break
		}

		// Rate limit - retry with exponential backoff
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries-1 {
			resp.Body.Close()
			backoff := time.Duration(1<<uint(attempt)) * time.Second // 1s, 2s, 4s
			time.Sleep(backoff)
			continue
		}

		// Other errors - return immediately
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("Drive API error (status %d): %s", resp.StatusCode, string(body))
	}

	defer resp.Body.Close()

	// Parse response
	var result struct {
		StorageQuota struct {
			Limit string `json:"limit"` // Can be string or number
			Usage string `json:"usage"`
		} `json:"storageQuota"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Parse limit and usage (handle both string and number formats)
	var limit, usage int64
	if result.StorageQuota.Limit != "" {
		fmt.Sscanf(result.StorageQuota.Limit, "%d", &limit)
	}
	if result.StorageQuota.Usage != "" {
		fmt.Sscanf(result.StorageQuota.Usage, "%d", &usage)
	}

	return &DriveStorageInfo{
		Limit: limit,
		Usage: usage,
	}, nil
}
