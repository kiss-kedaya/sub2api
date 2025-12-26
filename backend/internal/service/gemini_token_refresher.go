package service

import (
	"context"
	"strconv"
	"time"
)

type GeminiTokenRefresher struct {
	geminiOAuthService *GeminiOAuthService
}

func NewGeminiTokenRefresher(geminiOAuthService *GeminiOAuthService) *GeminiTokenRefresher {
	return &GeminiTokenRefresher{geminiOAuthService: geminiOAuthService}
}

func (r *GeminiTokenRefresher) CanRefresh(account *Account) bool {
	return account.Platform == PlatformGemini && account.Type == AccountTypeOAuth
}

func (r *GeminiTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if !r.CanRefresh(account) {
		return false
	}
	expiresAtStr := account.GetCredential("expires_at")
	if expiresAtStr == "" {
		return false
	}
	expiresAt, err := strconv.ParseInt(expiresAtStr, 10, 64)
	if err != nil {
		return false
	}
	expiryTime := time.Unix(expiresAt, 0)
	return time.Until(expiryTime) < refreshWindow
}

func (r *GeminiTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	tokenInfo, err := r.geminiOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}

	newCredentials := r.geminiOAuthService.BuildAccountCredentials(tokenInfo)
	for k, v := range account.Credentials {
		if _, exists := newCredentials[k]; !exists {
			newCredentials[k] = v
		}
	}

	return newCredentials, nil
}
