package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
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
	Provider          string
	APIKey            string
	Endpoint          string
	ProxyURL          string
	AllowPrivate      bool
	AllowInsecureHTTP bool
}

// UpstreamCaptchaSolver solves a Cloudflare Turnstile challenge.
type UpstreamCaptchaSolver interface {
	SolveTurnstile(ctx context.Context, siteKey, pageURL string) (string, error)
}

type upstreamCaptchaTaskSolver struct {
	provider          string
	apiKey            string
	endpoint          string
	proxyURL          string
	allowPrivate      bool
	allowInsecureHTTP bool
	taskType          string
	client            *http.Client
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
	if custom := strings.TrimRight(strings.Trim(cfg.Endpoint, " "), "/"); custom != "" {
		endpoint = custom
	}
	validatedEndpoint, err := validateUpstreamCaptchaEndpointLiteral(endpoint, cfg.AllowInsecureHTTP, cfg.AllowPrivate)
	if err != nil {
		return nil, err
	}
	endpoint = validatedEndpoint

	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           urlvalidator.NewSafeDialContext(urlvalidator.OutboundPolicy{AllowPrivate: cfg.AllowPrivate}),
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	var validatedProxy string
	if proxy := strings.Trim(cfg.ProxyURL, " "); proxy != "" {
		checkedProxy, proxyErr := validateUpstreamProxyURLLiteral(proxy, cfg.AllowPrivate)
		if proxyErr != nil {
			return nil, proxyErr
		}
		parsed, parseErr := url.Parse(checkedProxy)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid captcha proxy url: %w", parseErr)
		}
		validatedProxy = checkedProxy
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &upstreamCaptchaTaskSolver{
		provider:          provider,
		apiKey:            strings.TrimSpace(cfg.APIKey),
		endpoint:          endpoint,
		proxyURL:          validatedProxy,
		allowPrivate:      cfg.AllowPrivate,
		allowInsecureHTTP: cfg.AllowInsecureHTTP,
		taskType:          taskType,
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// Captcha endpoints carry clientKey in POST payloads and must never follow redirects.
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func containsControlOrBackslash(raw string) bool {
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b < 0x20 || b == 0x7f || b == '\\' {
			return true
		}
	}
	return false
}

func isBlockedMetadataHostname(host string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	switch normalized {
	case "metadata.google.internal", "metadata.goog", "instance-data":
		return true
	default:
		return false
	}
}

func validateUpstreamCaptchaEndpointLiteral(raw string, allowInsecureHTTP, allowPrivate bool) (string, error) {
	if containsControlOrBackslash(raw) {
		return "", errors.New("invalid captcha endpoint url")
	}
	trimmed := strings.Trim(raw, " ")
	if trimmed == "" {
		return "", errors.New("invalid captcha endpoint url")
	}
	normalized, err := urlvalidator.ValidateHTTPURL(trimmed, allowInsecureHTTP, urlvalidator.ValidationOptions{
		AllowPrivate: allowPrivate,
	})
	if err != nil {
		return "", fmt.Errorf("captcha endpoint target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("captcha endpoint target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
	}
	if !allowPrivate {
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(parsed.Hostname())), ".")
		if urlvalidator.IsBlockedHost(host) || isBlockedMetadataHostname(host) {
			return "", fmt.Errorf("captcha endpoint target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
		}
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil && !urlvalidator.IsPublicResolvedIP(ip) {
			return "", fmt.Errorf("captcha endpoint target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
		}
	}
	return normalized, nil
}

// validateUpstreamProxyURLLiteral validates proxy URL syntax, scheme, port,
// and literal private/loopback/metadata hosts without performing DNS lookups.
func validateUpstreamProxyURLLiteral(raw string, allowPrivate bool) (string, error) {
	if containsControlOrBackslash(raw) {
		return "", errors.New("invalid proxy url: contains control characters or backslash")
	}
	trimmed := strings.Trim(raw, " ")
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" || parsed.Fragment != "" {
		return "", errors.New("invalid proxy url")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("invalid proxy url scheme %q", parsed.Scheme)
	}
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(parsed.Hostname())), ".")
	if host == "" {
		return "", errors.New("invalid proxy url: missing host")
	}
	if port := parsed.Port(); port != "" {
		num, portErr := strconv.Atoi(port)
		if portErr != nil || num <= 0 || num > 65535 {
			return "", errors.New("invalid proxy url port")
		}
	}
	if !allowPrivate {
		if urlvalidator.IsBlockedHost(host) || isBlockedMetadataHostname(host) {
			return "", fmt.Errorf("proxy url target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
		}
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil && !urlvalidator.IsPublicResolvedIP(ip) {
			return "", fmt.Errorf("proxy url target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
		}
	}
	return trimmed, nil
}

// validateUpstreamProxyURL validates an operator-supplied upstream proxy URL
// (scheme, host, port, literal IP checks, and DNS resolution via
// urlvalidator.ValidateOutboundHost when allowPrivate is false).
func validateUpstreamProxyURL(ctx context.Context, raw string, allowPrivate bool) (string, error) {
	trimmed, err := validateUpstreamProxyURLLiteral(raw, allowPrivate)
	if err != nil || trimmed == "" || allowPrivate {
		return trimmed, err
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", errors.New("invalid proxy url")
	}
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(parsed.Hostname())), ".")
	if _, parseErr := netip.ParseAddr(host); parseErr != nil {
		if err := urlvalidator.ValidateOutboundHost(ctx, host, false); err != nil {
			return "", fmt.Errorf("proxy url target rejected by URL security policy: %w", urlvalidator.ErrOutboundTargetNotAllowed)
		}
	}
	return trimmed, nil
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
	if _, err := urlvalidator.ValidateOutboundURL(ctx, s.endpoint, urlvalidator.OutboundPolicy{
		AllowPrivate:      s.allowPrivate,
		AllowInsecureHTTP: s.allowInsecureHTTP,
	}); err != nil {
		return nil, fmt.Errorf("%s outbound target rejected: %w", s.provider, err)
	}
	if s.proxyURL != "" {
		if _, err := validateUpstreamProxyURL(ctx, s.proxyURL, s.allowPrivate); err != nil {
			return nil, fmt.Errorf("%s proxy rejected: %w", s.provider, err)
		}
	}
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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s HTTP %d", s.provider, resp.StatusCode)
	}
	return body, nil
}
