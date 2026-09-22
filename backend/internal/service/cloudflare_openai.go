package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	cloudflareModelsPerPage   = 50
	cloudflareModelsMaxPages  = 4
	cloudflareModelsBodyLimit = 2 << 20
)

var cloudflareOpenAIAPIRoot = "https://api.cloudflare.com/client/v4/accounts/"

var cloudflareAccountIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

func (a *Account) IsCloudflareOpenAI() bool {
	return a != nil && a.Platform == PlatformOpenAI && a.Type == AccountTypeCloudflare
}

func (a *Account) CloudflareAccountID() (string, error) {
	if a == nil {
		return "", fmt.Errorf("cloudflare account is required")
	}
	accountID := strings.TrimSpace(a.GetCredential("account_id"))
	if !cloudflareAccountIDPattern.MatchString(accountID) {
		return "", fmt.Errorf("cloudflare account id must be 8-64 characters of letters, digits, underscore, or hyphen")
	}
	return accountID, nil
}

func cloudflareOpenAIBaseURL(accountID string) string {
	return cloudflareOpenAIAPIRoot + accountID + "/ai/v1"
}

func cloudflareModelsSearchURL(accountID string, page, perPage int) string {
	values := url.Values{}
	values.Set("page", fmt.Sprintf("%d", page))
	values.Set("per_page", fmt.Sprintf("%d", perPage))
	return cloudflareOpenAIAPIRoot + accountID + "/ai/models/search?" + values.Encode()
}

func validateCloudflareAccount(account *Account) error {
	if account == nil || account.Type != AccountTypeCloudflare {
		return nil
	}
	if account.Platform != PlatformOpenAI {
		return fmt.Errorf("cloudflare account type requires openai platform")
	}
	if _, err := account.CloudflareAccountID(); err != nil {
		return err
	}
	if strings.TrimSpace(account.GetCredential("api_key")) == "" {
		return fmt.Errorf("cloudflare api token is required")
	}
	return nil
}

func isStaticCredentialAccountType(accountType string) bool {
	return accountType == AccountTypeAPIKey || accountType == AccountTypeCloudflare
}

func cloudflareChatOnlyUpstreamError() error {
	return fmt.Errorf("cloudflare workers ai only supports chat completions")
}

func oauthOnlyGroupError(groupName, accountType string) error {
	if accountType == AccountTypeAPIKey {
		return fmt.Errorf("分组 [%s] 仅允许 OAuth 账号，apikey 类型账号无法加入", groupName)
	}
	return fmt.Errorf("分组 [%s] 仅允许 OAuth 账号，%s 类型账号无法加入", groupName, accountType)
}

type cloudflareModelSearchPage struct {
	Success bool `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Result []struct {
		Name string `json:"name"`
	} `json:"result"`
	ResultInfo struct {
		TotalCount int `json:"total_count"`
		Count      int `json:"count"`
	} `json:"result_info"`
}

func parseCloudflareModelSearch(body []byte) ([]string, int, error) {
	var page cloudflareModelSearchPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, 0, fmt.Errorf("cloudflare model list was not valid JSON")
	}
	if !page.Success {
		message := "cloudflare model list request failed"
		if len(page.Errors) > 0 && strings.TrimSpace(page.Errors[0].Message) != "" {
			message = strings.TrimSpace(page.Errors[0].Message)
		}
		return nil, 0, fmt.Errorf("%s", message)
	}
	names := make([]string, 0, len(page.Result))
	seen := make(map[string]struct{}, len(page.Result))
	for _, item := range page.Result {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	total := page.ResultInfo.TotalCount
	if total <= 0 {
		total = page.ResultInfo.Count
	}
	return names, total, nil
}

func fetchCloudflareModelNames(ctx context.Context, upstream HTTPUpstream, account *Account) ([]string, error) {
	if upstream == nil {
		return nil, newUpstreamModelSyncConfigError("Upstream HTTP client is not configured", nil)
	}
	if err := validateCloudflareAccount(account); err != nil {
		return nil, newUpstreamModelSyncConfigError(err.Error(), err)
	}
	accountID, err := account.CloudflareAccountID()
	if err != nil {
		return nil, newUpstreamModelSyncConfigError(err.Error(), err)
	}
	apiKey := strings.TrimSpace(account.GetCredential("api_key"))
	seen := make(map[string]struct{})
	names := make([]string, 0, cloudflareModelsPerPage)
	total := 0
	for page := 1; page <= cloudflareModelsMaxPages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudflareModelsSearchURL(accountID, page, cloudflareModelsPerPage), nil)
		if err != nil {
			return nil, newUpstreamModelSyncConfigError("Invalid Cloudflare model list URL", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		account.ApplyHeaderOverrides(req.Header)

		resp, err := upstream.Do(req, upstreamModelsProxyURL(account), account.ID, account.Concurrency)
		if err != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to request Cloudflare model list", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, cloudflareModelsBodyLimit+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, newUpstreamModelSyncUpstreamError("Failed to read Cloudflare model list", readErr)
		}
		if int64(len(body)) > cloudflareModelsBodyLimit {
			return nil, newUpstreamModelSyncUpstreamError("Cloudflare model list response is too large", fmt.Errorf("response exceeds %d bytes", cloudflareModelsBodyLimit))
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, &UpstreamModelSyncError{
				Kind:       UpstreamModelSyncErrorUpstream,
				Message:    fmt.Sprintf("Cloudflare model list request failed with HTTP %d", resp.StatusCode),
				StatusCode: resp.StatusCode,
				Err:        fmt.Errorf("cloudflare model list returned HTTP %d", resp.StatusCode),
			}
		}
		pageNames, pageTotal, parseErr := parseCloudflareModelSearch(body)
		if parseErr != nil {
			return nil, newUpstreamModelSyncUpstreamError(parseErr.Error(), parseErr)
		}
		if page == 1 {
			total = pageTotal
		}
		for _, name := range pageNames {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		if len(pageNames) == 0 || (total > 0 && len(names) >= total) || (total <= 0 && len(pageNames) < cloudflareModelsPerPage) {
			break
		}
	}
	if len(names) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Upstream returned no supported models", nil)
	}
	return names, nil
}
