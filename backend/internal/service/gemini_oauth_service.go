package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/service/ports"
)

type GeminiOAuthService struct {
	sessionStore *geminicli.SessionStore
	proxyRepo    ports.ProxyRepository
	oauthClient  ports.GeminiOAuthClient
	codeAssist   ports.GeminiCliCodeAssistClient
	cfg          *config.Config
}

func NewGeminiOAuthService(
	proxyRepo ports.ProxyRepository,
	oauthClient ports.GeminiOAuthClient,
	codeAssist ports.GeminiCliCodeAssistClient,
	cfg *config.Config,
) *GeminiOAuthService {
	return &GeminiOAuthService{
		sessionStore: geminicli.NewSessionStore(),
		proxyRepo:    proxyRepo,
		oauthClient:  oauthClient,
		codeAssist:   codeAssist,
		cfg:          cfg,
	}
}

type GeminiAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

func (s *GeminiOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI string) (*GeminiAuthURLResult, error) {
	state, err := geminicli.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}
	codeVerifier, err := geminicli.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate code verifier: %w", err)
	}
	codeChallenge := geminicli.GenerateCodeChallenge(codeVerifier)
	sessionID, err := geminicli.GenerateSessionID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}

	var proxyURL string
	if proxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	session := &geminicli.OAuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ProxyURL:     proxyURL,
		RedirectURI:  redirectURI,
		CreatedAt:    time.Now(),
	}
	s.sessionStore.Set(sessionID, session)

	oauthCfg := geminicli.OAuthConfig{
		ClientID:     s.cfg.Gemini.OAuth.ClientID,
		ClientSecret: s.cfg.Gemini.OAuth.ClientSecret,
		Scopes:       s.cfg.Gemini.OAuth.Scopes,
	}

	authURL, err := geminicli.BuildAuthorizationURL(oauthCfg, state, codeChallenge, redirectURI)
	if err != nil {
		return nil, err
	}

	return &GeminiAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
		State:     state,
	}, nil
}

type GeminiExchangeCodeInput struct {
	SessionID   string
	State       string
	Code        string
	RedirectURI string
	ProxyID     *int64
}

type GeminiTokenInfo struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope,omitempty"`
	ProjectID    string `json:"project_id,omitempty"`
}

func (s *GeminiOAuthService) ExchangeCode(ctx context.Context, input *GeminiExchangeCodeInput) (*GeminiTokenInfo, error) {
	session, ok := s.sessionStore.Get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}
	if strings.TrimSpace(input.State) == "" || input.State != session.State {
		return nil, fmt.Errorf("invalid state")
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *input.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	redirectURI := session.RedirectURI
	if strings.TrimSpace(input.RedirectURI) != "" {
		redirectURI = input.RedirectURI
	}

	tokenResp, err := s.oauthClient.ExchangeCode(ctx, input.Code, session.CodeVerifier, redirectURI, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	s.sessionStore.Delete(input.SessionID)

	// 计算过期时间时减去 5 分钟安全时间窗口,考虑网络延迟和时钟偏差
	expiresAt := time.Now().Unix() + tokenResp.ExpiresIn - 300
	projectID, _ := s.fetchProjectID(ctx, tokenResp.AccessToken, proxyURL)

	return &GeminiTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    expiresAt,
		Scope:        tokenResp.Scope,
		ProjectID:    projectID,
	}, nil
}

func (s *GeminiOAuthService) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*GeminiTokenInfo, error) {
	var lastErr error

	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			time.Sleep(backoff)
		}

		tokenResp, err := s.oauthClient.RefreshToken(ctx, refreshToken, proxyURL)
		if err == nil {
			// 计算过期时间时减去 5 分钟安全时间窗口,考虑网络延迟和时钟偏差
			expiresAt := time.Now().Unix() + tokenResp.ExpiresIn - 300
			return &GeminiTokenInfo{
				AccessToken:  tokenResp.AccessToken,
				RefreshToken: tokenResp.RefreshToken,
				TokenType:    tokenResp.TokenType,
				ExpiresIn:    tokenResp.ExpiresIn,
				ExpiresAt:    expiresAt,
				Scope:        tokenResp.Scope,
			}, nil
		}

		if isNonRetryableGeminiOAuthError(err) {
			return nil, err
		}
		lastErr = err
	}

	return nil, fmt.Errorf("token refresh failed after retries: %w", lastErr)
}

func isNonRetryableGeminiOAuthError(err error) bool {
	msg := err.Error()
	nonRetryable := []string{
		"invalid_grant",
		"invalid_client",
		"unauthorized_client",
		"access_denied",
	}
	for _, needle := range nonRetryable {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func (s *GeminiOAuthService) RefreshAccountToken(ctx context.Context, account *model.Account) (*GeminiTokenInfo, error) {
	if account.Platform != model.PlatformGemini || account.Type != model.AccountTypeOAuth {
		return nil, fmt.Errorf("account is not a Gemini OAuth account")
	}

	refreshToken := account.GetCredential("refresh_token")
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	var proxyURL string
	if account.ProxyID != nil {
		proxy, err := s.proxyRepo.GetByID(ctx, *account.ProxyID)
		if err == nil && proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	return s.RefreshToken(ctx, refreshToken, proxyURL)
}

func (s *GeminiOAuthService) BuildAccountCredentials(tokenInfo *GeminiTokenInfo) map[string]any {
	creds := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   strconv.FormatInt(tokenInfo.ExpiresAt, 10),
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.TokenType != "" {
		creds["token_type"] = tokenInfo.TokenType
	}
	if tokenInfo.Scope != "" {
		creds["scope"] = tokenInfo.Scope
	}
	if tokenInfo.ProjectID != "" {
		creds["project_id"] = tokenInfo.ProjectID
	}
	return creds
}

func (s *GeminiOAuthService) Stop() {
	s.sessionStore.Stop()
}

func (s *GeminiOAuthService) fetchProjectID(ctx context.Context, accessToken, proxyURL string) (string, error) {
	if s.codeAssist == nil {
		return "", errors.New("code assist client not configured")
	}

	loadResp, err := s.codeAssist.LoadCodeAssist(ctx, accessToken, proxyURL, nil)
	if err == nil && strings.TrimSpace(loadResp.CurrentTier) != "" && strings.TrimSpace(loadResp.CloudAICompanionProject) != "" {
		return strings.TrimSpace(loadResp.CloudAICompanionProject), nil
	}

	// pick default tier from allowedTiers, fallback to LEGACY.
	tierID := "LEGACY"
	if loadResp != nil {
		for _, tier := range loadResp.AllowedTiers {
			if tier.IsDefault && strings.TrimSpace(tier.ID) != "" {
				tierID = tier.ID
				break
			}
		}
	}

	req := &geminicli.OnboardUserRequest{
		TierID: tierID,
		Metadata: geminicli.LoadCodeAssistMetadata{
			IDEType:    "ANTIGRAVITY",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
	}

	maxAttempts := 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := s.codeAssist.OnboardUser(ctx, accessToken, proxyURL, req)
		if err != nil {
			return "", err
		}
		if resp.Done {
			if resp.Response == nil || resp.Response.CloudAICompanionProject == nil {
				return "", errors.New("onboardUser completed but no project_id returned")
			}
			switch v := resp.Response.CloudAICompanionProject.(type) {
			case string:
				return strings.TrimSpace(v), nil
			case map[string]any:
				if id, ok := v["id"].(string); ok {
					return strings.TrimSpace(id), nil
				}
			}
			return "", errors.New("onboardUser returned unsupported project_id format")
		}
		time.Sleep(2 * time.Second)
	}

	return "", fmt.Errorf("onboardUser timeout after %d attempts", maxAttempts)
}
