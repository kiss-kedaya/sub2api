package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Turnstile solving for managed upstream logins. Four providers share the same
// createTask/getTaskResult JSON protocol; only the task type and default
// endpoint differ. Nothing here is used unless the upstream reports
// turnstile_enabled and the channel has a captcha key configured.
const (
	UpstreamCaptchaProviderCapSolver   = "capsolver"
	UpstreamCaptchaProviderTwoCaptcha  = "2captcha"
	UpstreamCaptchaProviderAntiCaptcha = "anticaptcha"
	UpstreamCaptchaProviderYesCaptcha  = "yescaptcha"

	upstreamCaptchaPollInterval = 2 * time.Second
	upstreamCaptchaTimeout      = 120 * time.Second
)

// UpstreamCaptchaConfig is one provider configuration.
type UpstreamCaptchaConfig struct {
	Provider string
	APIKey   string
	Endpoint string
	ProxyURL string
}

// UpstreamCaptchaSolver solves a Cloudflare Turnstile challenge.
type UpstreamCaptchaSolver interface {
	SolveTurnstile(ctx context.Context, siteKey, pageURL string) (string, error)
}

type upstreamCaptchaTaskSolver struct {
	provider string
	apiKey   string
	endpoint string
	taskType string
	client   *http.Client
}

// NewUpstreamCaptchaSolver builds a solver for the configured provider.
func NewUpstreamCaptchaSolver(cfg UpstreamCaptchaConfig) (UpstreamCaptchaSolver, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	var endpoint, taskType string
	switch provider {
	case UpstreamCaptchaProviderCapSolver:
		endpoint, taskType = "https://api.capsolver.com", "AntiTurnstileTaskProxyLess"
	case UpstreamCaptchaProviderTwoCaptcha:
		endpoint, taskType = "https://api.2captcha.com", "TurnstileTaskProxyless"
	case UpstreamCaptchaProviderAntiCaptcha:
		endpoint, taskType = "https://api.anti-captcha.com", "TurnstileTaskProxyless"
	case UpstreamCaptchaProviderYesCaptcha:
		endpoint, taskType = "https://api.yescaptcha.com", "TurnstileTaskProxyless"
	default:
		return nil, fmt.Errorf("unknown captcha provider %q", cfg.Provider)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("captcha api key is empty")
	}
	if custom := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/"); custom != "" {
		endpoint = custom
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second}
	if proxy := strings.TrimSpace(cfg.ProxyURL); proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid captcha proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &upstreamCaptchaTaskSolver{
		provider: provider,
		apiKey:   strings.TrimSpace(cfg.APIKey),
		endpoint: endpoint,
		taskType: taskType,
		client:   &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}, nil
}

type upstreamCaptchaCreateResponse struct {
	ErrorID          int             `json:"errorId"`
	ErrorCode        string          `json:"errorCode"`
	ErrorDescription string          `json:"errorDescription"`
	TaskID           json.RawMessage `json:"taskId"`
}

type upstreamCaptchaResultResponse struct {
	ErrorID          int    `json:"errorId"`
	ErrorCode        string `json:"errorCode"`
	ErrorDescription string `json:"errorDescription"`
	Status           string `json:"status"`
	Solution         struct {
		Token string `json:"token"`
	} `json:"solution"`
}

func (s *upstreamCaptchaTaskSolver) SolveTurnstile(ctx context.Context, siteKey, pageURL string) (string, error) {
	if strings.TrimSpace(siteKey) == "" {
		return "", errors.New("turnstile site key is empty")
	}
	taskID, err := s.createTask(ctx, siteKey, pageURL)
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(upstreamCaptchaTimeout)
	ticker := time.NewTicker(upstreamCaptchaPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("%s: timed out waiting for a turnstile token", s.provider)
			}
			token, ready, err := s.fetchResult(ctx, taskID)
			if err != nil {
				return "", err
			}
			if ready {
				return token, nil
			}
		}
	}
}

func (s *upstreamCaptchaTaskSolver) createTask(ctx context.Context, siteKey, pageURL string) (string, error) {
	body := map[string]any{
		"clientKey": s.apiKey,
		"task": map[string]any{
			"type":       s.taskType,
			"websiteURL": pageURL,
			"websiteKey": siteKey,
		},
	}
	raw, err := s.post(ctx, "/createTask", body)
	if err != nil {
		return "", err
	}
	var parsed upstreamCaptchaCreateResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("%s createTask decode: %w", s.provider, err)
	}
	if parsed.ErrorID != 0 {
		return "", fmt.Errorf("%s createTask: %s %s", s.provider, parsed.ErrorCode, parsed.ErrorDescription)
	}
	taskID := strings.Trim(strings.TrimSpace(string(parsed.TaskID)), "\"")
	if taskID == "" || taskID == "null" {
		return "", fmt.Errorf("%s createTask: empty task id", s.provider)
	}
	return taskID, nil
}

func (s *upstreamCaptchaTaskSolver) fetchResult(ctx context.Context, taskID string) (string, bool, error) {
	raw, err := s.post(ctx, "/getTaskResult", map[string]any{"clientKey": s.apiKey, "taskId": taskID})
	if err != nil {
		return "", false, err
	}
	var parsed upstreamCaptchaResultResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", false, fmt.Errorf("%s getTaskResult decode: %w", s.provider, err)
	}
	if parsed.ErrorID != 0 {
		return "", false, fmt.Errorf("%s getTaskResult: %s %s", s.provider, parsed.ErrorCode, parsed.ErrorDescription)
	}
	if parsed.Status == "ready" {
		if strings.TrimSpace(parsed.Solution.Token) == "" {
			return "", false, fmt.Errorf("%s: ready without a token", s.provider)
		}
		return parsed.Solution.Token, true, nil
	}
	return "", false, nil
}

func (s *upstreamCaptchaTaskSolver) post(ctx context.Context, path string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s encode: %w", s.provider, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("%s build request: %w", s.provider, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s http: %w", s.provider, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, fmt.Errorf("%s read: %w", s.provider, readErr)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s HTTP %d", s.provider, resp.StatusCode)
	}
	return body, nil
}
