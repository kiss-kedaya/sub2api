package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// UpstreamChannelClient speaks the public sub2api user API of a managed
// upstream site. It is deliberately transport-only: no database, no settings,
// no goroutines. The service layer owns sessions, encryption and scheduling.
const (
	upstreamChannelRequestTimeout = 30 * time.Second
	upstreamChannelMaxBodyBytes   = 4 << 20
	upstreamChannelErrorMaxLen    = 400
)

// ErrUpstreamChannelRequires2FA is returned when the upstream account still
// requires a second factor. Managed channels must use an account without 2FA.
var ErrUpstreamChannelRequires2FA = errors.New("upstream account requires 2FA; use an account without 2FA")

// UpstreamChannelSession is one authenticated upstream session.
type UpstreamChannelSession struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// Expired reports whether the access token needs a refresh.
func (s UpstreamChannelSession) Expired(now time.Time) bool {
	if strings.TrimSpace(s.AccessToken) == "" {
		return true
	}
	if s.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(time.Minute).Before(s.ExpiresAt)
}

// UpstreamAnnouncement is one upstream announcement.
type UpstreamAnnouncement struct {
	SourceKey       string
	Title           string
	Content         string
	NotifyMode      string
	PublishedAt     *time.Time
	SourceUpdatedAt *time.Time
}

// UpstreamAPIKey is one upstream API key row.
type UpstreamAPIKey struct {
	ID          int64      `json:"id"`
	Key         string     `json:"key,omitempty"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	GroupID     *int64     `json:"group_id"`
	GroupName   string     `json:"group_name,omitempty"`
	GroupRatio  float64    `json:"group_ratio,omitempty"`
	Quota       float64    `json:"quota"`
	QuotaUsed   float64    `json:"quota_used"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	IPWhitelist []string   `json:"ip_whitelist"`
	IPBlacklist []string   `json:"ip_blacklist"`
	RateLimit5h float64    `json:"rate_limit_5h"`
	RateLimit1d float64    `json:"rate_limit_1d"`
	RateLimit7d float64    `json:"rate_limit_7d"`
	Usage5h     float64    `json:"usage_5h"`
	Usage1d     float64    `json:"usage_1d"`
	Usage7d     float64    `json:"usage_7d"`
}

// UpstreamAPIKeyPage is one page of upstream API keys.
type UpstreamAPIKeyPage struct {
	Items    []UpstreamAPIKey `json:"items"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Pages    int              `json:"pages"`
}

// UpstreamAPIKeyGroup is one group the upstream account can bind keys to.
type UpstreamAPIKeyGroup struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Ratio       float64 `json:"ratio"`
}

// UpstreamAPIKeyQuery filters the key list.
type UpstreamAPIKeyQuery struct {
	Page     int
	PageSize int
	Search   string
	Status   string
	GroupID  string
}

// UpstreamAPIKeyCreateRequest mirrors the upstream create payload.
type UpstreamAPIKeyCreateRequest struct {
	Name          string
	GroupID       *int64
	CustomKey     string
	Quota         *float64
	ExpiresInDays *int
	RateLimit5h   *float64
	RateLimit1d   *float64
	RateLimit7d   *float64
	IPWhitelist   []string
	IPBlacklist   []string
}

// UpstreamAPIKeyUpdateRequest mirrors the upstream update payload.
type UpstreamAPIKeyUpdateRequest struct {
	Name                *string
	GroupID             *int64
	Status              *string
	Quota               *float64
	ExpiresAt           *string
	IPWhitelist         *[]string
	IPBlacklist         *[]string
	ResetQuota          *bool
	RateLimit5h         *float64
	RateLimit1d         *float64
	RateLimit7d         *float64
	ResetRateLimitUsage *bool
}

// UpstreamRechargeMethod is one usable payment method.
type UpstreamRechargeMethod struct {
	Type      string  `json:"type"`
	Name      string  `json:"name"`
	MinAmount float64 `json:"min_amount"`
	MaxAmount float64 `json:"max_amount"`
}

// UpstreamRechargeInfo is the recharge form metadata.
type UpstreamRechargeInfo struct {
	MinAmount         float64                  `json:"min_amount"`
	MaxAmount         float64                  `json:"max_amount"`
	Methods           []UpstreamRechargeMethod `json:"methods"`
	HelpText          string                   `json:"help_text,omitempty"`
	HelpImageURL      string                   `json:"help_image_url,omitempty"`
	AlipayForceQRCode bool                     `json:"alipay_force_qrcode"`
}

// UpstreamRechargeRequest creates a balance order.
type UpstreamRechargeRequest struct {
	Amount        float64
	PaymentMethod string
	IsMobile      bool
}

// UpstreamPaymentLaunch is what the upstream returns for a created order.
type UpstreamPaymentLaunch struct {
	PayURL      string     `json:"pay_url,omitempty"`
	QRCode      string     `json:"qr_code,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ResultType  string     `json:"result_type,omitempty"`
	PaymentMode string     `json:"payment_mode,omitempty"`
}

// UpstreamSubscriptionPlan is one plan on sale upstream.
type UpstreamSubscriptionPlan struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Price           float64  `json:"price"`
	Currency        string   `json:"currency"`
	Validity        string   `json:"validity"`
	GroupName       string   `json:"group_name"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd,omitempty"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd,omitempty"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd,omitempty"`
	Features        []string `json:"features"`
	PaymentMethods  []string `json:"payment_methods"`
}

// UpstreamSubscriptionInfo is the subscription page metadata.
type UpstreamSubscriptionInfo struct {
	Plans   []UpstreamSubscriptionPlan `json:"plans"`
	Methods []UpstreamRechargeMethod   `json:"methods"`
}

// UpstreamSubscriptionRequest creates a subscription order.
type UpstreamSubscriptionRequest struct {
	PlanID        string
	PaymentMethod string
	IsMobile      bool
}

// UpstreamSubscriptionUsageWindow is one quota window of an active subscription.
type UpstreamSubscriptionUsageWindow struct {
	LimitUSD     float64    `json:"limit_usd"`
	UsedUSD      float64    `json:"used_usd"`
	RemainingUSD float64    `json:"remaining_usd"`
	Percentage   float64    `json:"percentage"`
	ResetsAt     *time.Time `json:"resets_at,omitempty"`
}

// UpstreamSubscriptionUsage is one active subscription.
type UpstreamSubscriptionUsage struct {
	ID            int64                            `json:"id"`
	GroupName     string                           `json:"group_name"`
	Status        string                           `json:"status"`
	ExpiresAt     *time.Time                       `json:"expires_at,omitempty"`
	ExpiresInDays int                              `json:"expires_in_days"`
	Daily         *UpstreamSubscriptionUsageWindow `json:"daily,omitempty"`
	Weekly        *UpstreamSubscriptionUsageWindow `json:"weekly,omitempty"`
	Monthly       *UpstreamSubscriptionUsageWindow `json:"monthly,omitempty"`
}

// UpstreamRedeemResult is the outcome of a redeem code.
type UpstreamRedeemResult struct {
	Message        string   `json:"message"`
	Type           string   `json:"type"`
	Value          float64  `json:"value"`
	NewBalance     *float64 `json:"new_balance,omitempty"`
	NewConcurrency *int     `json:"new_concurrency,omitempty"`
	GroupName      *string  `json:"group_name,omitempty"`
	ValidityDays   *int     `json:"validity_days,omitempty"`
}

// UpstreamChannelClient is the HTTP adapter.
type UpstreamChannelClient struct {
	now func() time.Time
	// urlCheck vets (and normalizes) the site address before every request. Nil
	// means no outbound policy is wired, which is how the tests run.
	urlCheck func(string) (string, error)
}

// NewUpstreamChannelClient builds a client.
func NewUpstreamChannelClient() *UpstreamChannelClient {
	return &UpstreamChannelClient{now: func() time.Time { return time.Now().UTC() }}
}

// SetURLCheck installs the outbound address check run before every request.
func (c *UpstreamChannelClient) SetURLCheck(check func(string) (string, error)) {
	if c != nil {
		c.urlCheck = check
	}
}

func (c *UpstreamChannelClient) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now().UTC()
	}
	return c.now()
}

func (c *UpstreamChannelClient) httpClient(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
	}
	if proxy := strings.TrimSpace(proxyURL); proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy url: %w", err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return &http.Client{
		Timeout:   upstreamChannelRequestTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Never follow a redirect with a bearer token attached.
			return http.ErrUseLastResponse
		},
	}, nil
}

type upstreamChannelEnvelope struct {
	Code    *int            `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decodeUpstreamChannelJSON(body []byte, out any) error {
	var envelope upstreamChannelEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Code != nil {
		if *envelope.Code != 0 {
			message := strings.TrimSpace(envelope.Message)
			if message == "" {
				message = "upstream rejected the request"
			}
			return errors.New(message)
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return nil
		}
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("upstream response decode: %w", err)
		}
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("upstream response decode: %w", err)
	}
	return nil
}

func upstreamChannelErrorMessage(body []byte) string {
	var envelope upstreamChannelEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil {
		if message := strings.TrimSpace(envelope.Message); message != "" {
			return truncateUpstreamChannelText(message)
		}
	}
	return truncateUpstreamChannelText(strings.TrimSpace(string(body)))
}

func truncateUpstreamChannelText(text string) string {
	if len(text) > upstreamChannelErrorMaxLen {
		return text[:upstreamChannelErrorMaxLen] + "..."
	}
	return text
}

func (c *UpstreamChannelClient) doJSON(
	ctx context.Context,
	siteURL, proxyURL, method, path string,
	session *UpstreamChannelSession,
	payload any,
) ([]byte, error) {
	site := strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if site == "" {
		return nil, errors.New("upstream site url is empty")
	}
	// The stored address is checked again right before each request: it may have
	// been saved before the operator tightened the outbound policy.
	if c.urlCheck != nil {
		checked, checkErr := c.urlCheck(site)
		if checkErr != nil {
			return nil, checkErr
		}
		site = strings.TrimRight(checked, "/")
	}
	client, err := c.httpClient(proxyURL)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if payload != nil {
		encoded, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return nil, fmt.Errorf("encode request: %w", marshalErr)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, site+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != nil && strings.TrimSpace(session.AccessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(session.AccessToken))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, upstreamChannelMaxBodyBytes+1))
	if readErr != nil {
		return nil, errors.New("upstream response read failed")
	}
	if len(body) > upstreamChannelMaxBodyBytes {
		return nil, errors.New("upstream response too large")
	}
	if resp.StatusCode >= http.StatusBadRequest {
		message := upstreamChannelErrorMessage(body)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream HTTP %d: %s", resp.StatusCode, message)
	}
	return body, nil
}

// PublicSettings reads whether the upstream login needs a Turnstile token.
func (c *UpstreamChannelClient) PublicSettings(ctx context.Context, siteURL, proxyURL string) (bool, string, error) {
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/settings/public", nil, nil)
	if err != nil {
		return false, "", err
	}
	var settings struct {
		TurnstileEnabled bool   `json:"turnstile_enabled"`
		TurnstileSiteKey string `json:"turnstile_site_key"`
	}
	if err := decodeUpstreamChannelJSON(body, &settings); err != nil {
		return false, "", err
	}
	return settings.TurnstileEnabled, strings.TrimSpace(settings.TurnstileSiteKey), nil
}

// Login authenticates one managed upstream account.
func (c *UpstreamChannelClient) Login(ctx context.Context, siteURL, username, password, turnstileToken, proxyURL string) (*UpstreamChannelSession, error) {
	body := map[string]any{
		"email":    strings.TrimSpace(username),
		"password": password,
	}
	if token := strings.TrimSpace(turnstileToken); token != "" {
		body["turnstile_token"] = token
	}
	raw, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodPost, "/api/v1/auth/login", nil, body)
	if err != nil {
		return nil, err
	}
	var data struct {
		Requires2FA  bool   `json:"requires_2fa"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := decodeUpstreamChannelJSON(raw, &data); err != nil {
		return nil, err
	}
	if data.Requires2FA {
		return nil, ErrUpstreamChannelRequires2FA
	}
	if strings.TrimSpace(data.AccessToken) == "" {
		return nil, errors.New("upstream login returned an empty access token")
	}
	expires := c.clock().Add(time.Duration(data.ExpiresIn) * time.Second)
	if data.ExpiresIn <= 0 {
		expires = c.clock().Add(time.Hour)
	}
	return &UpstreamChannelSession{
		AccessToken:  strings.TrimSpace(data.AccessToken),
		RefreshToken: strings.TrimSpace(data.RefreshToken),
		ExpiresAt:    expires,
	}, nil
}

// RefreshSession exchanges a refresh token for a new access token.
func (c *UpstreamChannelClient) RefreshSession(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) (*UpstreamChannelSession, error) {
	if session == nil || strings.TrimSpace(session.RefreshToken) == "" {
		return nil, errors.New("upstream session has no refresh token")
	}
	raw, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodPost, "/api/v1/auth/refresh", nil, map[string]string{
		"refresh_token": strings.TrimSpace(session.RefreshToken),
	})
	if err != nil {
		return nil, err
	}
	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := decodeUpstreamChannelJSON(raw, &data); err != nil {
		return nil, err
	}
	if strings.TrimSpace(data.AccessToken) == "" {
		return nil, errors.New("upstream refresh returned an empty access token")
	}
	expires := c.clock().Add(time.Duration(data.ExpiresIn) * time.Second)
	if data.ExpiresIn <= 0 {
		expires = c.clock().Add(time.Hour)
	}
	refresh := strings.TrimSpace(data.RefreshToken)
	if refresh == "" {
		refresh = strings.TrimSpace(session.RefreshToken)
	}
	return &UpstreamChannelSession{
		AccessToken:  strings.TrimSpace(data.AccessToken),
		RefreshToken: refresh,
		ExpiresAt:    expires,
	}, nil
}

// Balance reads the upstream account balance. Both /auth/me and /user/profile
// are supported because distributions differ.
func (c *UpstreamChannelClient) Balance(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) (float64, error) {
	var lastErr error
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/user/profile"} {
		body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, path, session, nil)
		if err != nil {
			lastErr = err
			continue
		}
		var me struct {
			Balance float64 `json:"balance"`
		}
		if err := decodeUpstreamChannelJSON(body, &me); err != nil {
			lastErr = err
			continue
		}
		return me.Balance, nil
	}
	if lastErr == nil {
		lastErr = errors.New("upstream balance unavailable")
	}
	return 0, lastErr
}

// Announcements lists the announcements visible to the managed account.
func (c *UpstreamChannelClient) Announcements(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) ([]UpstreamAnnouncement, error) {
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/announcements", session, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Content    string `json:"content"`
		NotifyMode string `json:"notify_mode"`
		StartsAt   string `json:"starts_at"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return nil, err
	}
	out := make([]UpstreamAnnouncement, 0, len(raw))
	for _, item := range raw {
		title := strings.TrimSpace(item.Title)
		content := strings.TrimSpace(item.Content)
		if item.ID == 0 && title == "" && content == "" {
			continue
		}
		key := strconv.FormatInt(item.ID, 10)
		if item.ID == 0 {
			key = UpstreamAnnouncementKey(title, content, item.CreatedAt, item.UpdatedAt)
		}
		out = append(out, UpstreamAnnouncement{
			SourceKey:       key,
			Title:           title,
			Content:         content,
			NotifyMode:      strings.TrimSpace(item.NotifyMode),
			PublishedAt:     parseUpstreamChannelTime(item.StartsAt, item.CreatedAt),
			SourceUpdatedAt: parseUpstreamChannelTime(item.UpdatedAt),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].PublishedAt, out[j].PublishedAt
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		return left.After(*right)
	})
	return out, nil
}

// UpstreamAnnouncementKey is the stable identity of an announcement without an id.
func UpstreamAnnouncementKey(title, content, createdAt, updatedAt string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{title, content, createdAt, updatedAt}, "\x00")))
	return "h:" + hex.EncodeToString(sum[:12])
}

func parseUpstreamChannelTime(values ...string) *time.Time {
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				utc := parsed.UTC()
				return &utc
			}
		}
	}
	return nil
}

// ListAPIKeys pages the upstream user keys.
func (c *UpstreamChannelClient) ListAPIKeys(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, query UpstreamAPIKeyQuery) (*UpstreamAPIKeyPage, error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	params := url.Values{}
	params.Set("page", strconv.Itoa(page))
	params.Set("page_size", strconv.Itoa(pageSize))
	params.Set("sort_by", "created_at")
	params.Set("sort_order", "desc")
	if search := strings.TrimSpace(query.Search); search != "" {
		params.Set("search", search)
	}
	if status := strings.TrimSpace(query.Status); status != "" {
		params.Set("status", upstreamChannelStatusForQuery(status))
	}
	if groupID := strings.TrimSpace(query.GroupID); groupID != "" {
		params.Set("group_id", groupID)
	}
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/keys?"+params.Encode(), session, nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Items    []map[string]any `json:"items"`
		Total    int64            `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"page_size"`
		Pages    int              `json:"pages"`
	}
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return nil, err
	}
	groups, _ := c.APIKeyGroups(ctx, siteURL, proxyURL, session)
	groupByID := make(map[int64]UpstreamAPIKeyGroup, len(groups))
	for _, group := range groups {
		groupByID[group.ID] = group
	}
	items := make([]UpstreamAPIKey, 0, len(raw.Items))
	for _, item := range raw.Items {
		decoded := decodeUpstreamAPIKey(item)
		if decoded.GroupID != nil {
			if group, ok := groupByID[*decoded.GroupID]; ok {
				decoded.GroupName = group.Name
				decoded.GroupRatio = group.Ratio
			}
		}
		items = append(items, decoded)
	}
	if raw.Page <= 0 {
		raw.Page = page
	}
	if raw.PageSize <= 0 {
		raw.PageSize = pageSize
	}
	if raw.Pages <= 0 && raw.PageSize > 0 {
		raw.Pages = int((raw.Total + int64(raw.PageSize) - 1) / int64(raw.PageSize))
	}
	return &UpstreamAPIKeyPage{
		Items:    items,
		Total:    raw.Total,
		Page:     raw.Page,
		PageSize: raw.PageSize,
		Pages:    raw.Pages,
	}, nil
}

type upstreamAPIKeyWire struct {
	ID      int64  `json:"id"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	GroupID *int64 `json:"group_id"`
	Group   *struct {
		ID             int64   `json:"id"`
		Name           string  `json:"name"`
		RateMultiplier float64 `json:"rate_multiplier"`
	} `json:"group"`
	Status      string     `json:"status"`
	IPWhitelist []string   `json:"ip_whitelist"`
	IPBlacklist []string   `json:"ip_blacklist"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	Quota       float64    `json:"quota"`
	QuotaUsed   float64    `json:"quota_used"`
	ExpiresAt   *time.Time `json:"expires_at"`
	CreatedAt   *time.Time `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
	RateLimit5h float64    `json:"rate_limit_5h"`
	RateLimit1d float64    `json:"rate_limit_1d"`
	RateLimit7d float64    `json:"rate_limit_7d"`
	Usage5h     float64    `json:"usage_5h"`
	Usage1d     float64    `json:"usage_1d"`
	Usage7d     float64    `json:"usage_7d"`
}

func decodeUpstreamAPIKey(item map[string]any) UpstreamAPIKey {
	encoded, _ := json.Marshal(item)
	var key upstreamAPIKeyWire
	_ = json.Unmarshal(encoded, &key)
	out := UpstreamAPIKey{
		ID: key.ID, Key: key.Key, Name: key.Name,
		Status:  normalizeUpstreamAPIKeyStatus(key.Status),
		GroupID: key.GroupID, Quota: key.Quota, QuotaUsed: key.QuotaUsed,
		ExpiresAt: key.ExpiresAt, CreatedAt: key.CreatedAt, UpdatedAt: key.UpdatedAt,
		LastUsedAt:  key.LastUsedAt,
		IPWhitelist: safeUpstreamStrings(key.IPWhitelist), IPBlacklist: safeUpstreamStrings(key.IPBlacklist),
		RateLimit5h: key.RateLimit5h, RateLimit1d: key.RateLimit1d, RateLimit7d: key.RateLimit7d,
		Usage5h: key.Usage5h, Usage1d: key.Usage1d, Usage7d: key.Usage7d,
	}
	if key.Group != nil {
		if out.GroupID == nil {
			id := key.Group.ID
			out.GroupID = &id
		}
		out.GroupName = key.Group.Name
		out.GroupRatio = key.Group.RateMultiplier
	}
	return out
}

func normalizeUpstreamAPIKeyStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "inactive":
		return "disabled"
	case "active", "disabled", "quota_exhausted", "expired":
		return strings.TrimSpace(status)
	default:
		return strings.TrimSpace(status)
	}
}

func upstreamChannelStatusForQuery(status string) string {
	if strings.EqualFold(strings.TrimSpace(status), "disabled") {
		return "inactive"
	}
	return strings.TrimSpace(status)
}

func safeUpstreamStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// APIKeyGroups lists groups the managed account may bind keys to.
func (c *UpstreamChannelClient) APIKeyGroups(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) ([]UpstreamAPIKeyGroup, error) {
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/groups/available", session, nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID             int64   `json:"id"`
		Name           string  `json:"name"`
		Description    string  `json:"description"`
		RateMultiplier float64 `json:"rate_multiplier"`
	}
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return nil, err
	}
	overrides := map[string]float64{}
	if ratesBody, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/groups/rates", session, nil); err == nil {
		_ = decodeUpstreamChannelJSON(ratesBody, &overrides)
	}
	out := make([]UpstreamAPIKeyGroup, 0, len(raw))
	for _, item := range raw {
		ratio := item.RateMultiplier
		if value, ok := overrides[strconv.FormatInt(item.ID, 10)]; ok {
			ratio = value
		}
		out = append(out, UpstreamAPIKeyGroup{ID: item.ID, Name: item.Name, Description: item.Description, Ratio: ratio})
	}
	return out, nil
}

// CreateAPIKey creates one upstream key.
func (c *UpstreamChannelClient) CreateAPIKey(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, req UpstreamAPIKeyCreateRequest) (*UpstreamAPIKey, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("api key name is required")
	}
	payload := map[string]any{
		"name":         strings.TrimSpace(req.Name),
		"ip_whitelist": safeUpstreamStrings(req.IPWhitelist),
		"ip_blacklist": safeUpstreamStrings(req.IPBlacklist),
	}
	if req.GroupID != nil {
		payload["group_id"] = *req.GroupID
	}
	if custom := strings.TrimSpace(req.CustomKey); custom != "" {
		payload["custom_key"] = custom
	}
	if req.Quota != nil {
		payload["quota"] = *req.Quota
	}
	if req.ExpiresInDays != nil {
		payload["expires_in_days"] = *req.ExpiresInDays
	}
	if req.RateLimit5h != nil {
		payload["rate_limit_5h"] = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		payload["rate_limit_1d"] = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		payload["rate_limit_7d"] = *req.RateLimit7d
	}
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodPost, "/api/v1/keys", session, payload)
	if err != nil {
		return nil, err
	}
	return decodeUpstreamAPIKeyResponse(body)
}

// UpdateAPIKey edits one upstream key.
func (c *UpstreamChannelClient) UpdateAPIKey(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, id int64, req UpstreamAPIKeyUpdateRequest) (*UpstreamAPIKey, error) {
	if id <= 0 {
		return nil, errors.New("api key id is invalid")
	}
	payload := map[string]any{}
	if req.Name != nil {
		payload["name"] = strings.TrimSpace(*req.Name)
	}
	if req.GroupID != nil {
		payload["group_id"] = *req.GroupID
	}
	if req.Status != nil {
		status := normalizeUpstreamAPIKeyStatus(*req.Status)
		if status != "active" && status != "disabled" {
			return nil, errors.New("only active or disabled can be set")
		}
		if status == "disabled" {
			payload["status"] = "inactive"
		} else {
			payload["status"] = status
		}
	}
	if req.Quota != nil {
		payload["quota"] = *req.Quota
	}
	if req.ExpiresAt != nil {
		payload["expires_at"] = strings.TrimSpace(*req.ExpiresAt)
	}
	if req.IPWhitelist != nil {
		payload["ip_whitelist"] = safeUpstreamStrings(*req.IPWhitelist)
	}
	if req.IPBlacklist != nil {
		payload["ip_blacklist"] = safeUpstreamStrings(*req.IPBlacklist)
	}
	if req.ResetQuota != nil {
		payload["reset_quota"] = *req.ResetQuota
	}
	if req.RateLimit5h != nil {
		payload["rate_limit_5h"] = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		payload["rate_limit_1d"] = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		payload["rate_limit_7d"] = *req.RateLimit7d
	}
	if req.ResetRateLimitUsage != nil {
		payload["reset_rate_limit_usage"] = *req.ResetRateLimitUsage
	}
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodPut, "/api/v1/keys/"+strconv.FormatInt(id, 10), session, payload)
	if err != nil {
		return nil, err
	}
	return decodeUpstreamAPIKeyResponse(body)
}

func decodeUpstreamAPIKeyResponse(body []byte) (*UpstreamAPIKey, error) {
	var raw map[string]any
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return nil, err
	}
	key := decodeUpstreamAPIKey(raw)
	return &key, nil
}

// DeleteAPIKey removes one upstream key.
func (c *UpstreamChannelClient) DeleteAPIKey(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, id int64) error {
	if id <= 0 {
		return errors.New("api key id is invalid")
	}
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodDelete, "/api/v1/keys/"+strconv.FormatInt(id, 10), session, nil)
	if err != nil {
		return err
	}
	var ignored any
	return decodeUpstreamChannelJSON(body, &ignored)
}

// RevealAPIKey reads the full key value once.
func (c *UpstreamChannelClient) RevealAPIKey(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, id int64) (string, error) {
	if id <= 0 {
		return "", errors.New("api key id is invalid")
	}
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/keys/"+strconv.FormatInt(id, 10), session, nil)
	if err != nil {
		return "", err
	}
	var raw struct {
		Key string `json:"key"`
	}
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return "", err
	}
	if strings.TrimSpace(raw.Key) == "" {
		return "", errors.New("upstream did not return a key value")
	}
	return raw.Key, nil
}

type upstreamCheckoutMethod struct {
	DailyLimit float64 `json:"daily_limit"`
	SingleMin  float64 `json:"single_min"`
	SingleMax  float64 `json:"single_max"`
	Available  *bool   `json:"available"`
}

func pickUpstreamMethod(methods map[string]upstreamCheckoutMethod, visibleType string) (upstreamCheckoutMethod, bool) {
	var first upstreamCheckoutMethod
	found := false
	for _, key := range []string{visibleType, visibleType + "_direct"} {
		value, ok := methods[key]
		if !ok {
			continue
		}
		if !found {
			first = value
			found = true
		}
		if value.Available == nil || *value.Available {
			return value, true
		}
	}
	return first, found
}

type upstreamCheckoutPlan struct {
	ID              int64    `json:"id"`
	GroupID         int64    `json:"group_id"`
	GroupName       string   `json:"group_name"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Price           float64  `json:"price"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`
	ValidityDays    int      `json:"validity_days"`
	ValidityUnit    string   `json:"validity_unit"`
	Features        []string `json:"features"`
}

type upstreamCheckoutPayload struct {
	Methods           map[string]upstreamCheckoutMethod `json:"methods"`
	GlobalMin         float64                           `json:"global_min"`
	GlobalMax         float64                           `json:"global_max"`
	HelpText          string                            `json:"help_text"`
	HelpImageURL      string                            `json:"help_image_url"`
	AlipayForceQRCode bool                              `json:"alipay_force_qrcode"`
	Plans             []upstreamCheckoutPlan            `json:"plans"`
}

func (c *UpstreamChannelClient) checkoutPayload(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) (*upstreamCheckoutPayload, error) {
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/payment/checkout-info", session, nil)
	if err != nil {
		return nil, err
	}
	var payload upstreamCheckoutPayload
	if err := decodeUpstreamChannelJSON(body, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// RechargeInfo reads the recharge form metadata.
func (c *UpstreamChannelClient) RechargeInfo(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) (*UpstreamRechargeInfo, error) {
	payload, err := c.checkoutPayload(ctx, siteURL, proxyURL, session)
	if err != nil {
		return nil, err
	}
	methods := make([]UpstreamRechargeMethod, 0, 2)
	add := func(visibleType, name string) {
		limit, ok := pickUpstreamMethod(payload.Methods, visibleType)
		if !ok || (limit.Available != nil && !*limit.Available) {
			return
		}
		methods = append(methods, UpstreamRechargeMethod{
			Type: visibleType, Name: name, MinAmount: limit.SingleMin, MaxAmount: limit.SingleMax,
		})
	}
	add("alipay", "支付宝")
	add("wxpay", "微信支付")
	if len(methods) == 0 {
		return nil, errors.New("upstream has no usable payment method")
	}
	return &UpstreamRechargeInfo{
		MinAmount:         payload.GlobalMin,
		MaxAmount:         payload.GlobalMax,
		Methods:           methods,
		HelpText:          payload.HelpText,
		HelpImageURL:      payload.HelpImageURL,
		AlipayForceQRCode: payload.AlipayForceQRCode,
	}, nil
}

// SubscriptionInfo reads the plans on sale upstream.
func (c *UpstreamChannelClient) SubscriptionInfo(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) (*UpstreamSubscriptionInfo, error) {
	payload, err := c.checkoutPayload(ctx, siteURL, proxyURL, session)
	if err != nil {
		return nil, err
	}
	methods := make([]UpstreamRechargeMethod, 0, 2)
	available := map[string]bool{}
	add := func(visibleType, name string) {
		limit, ok := pickUpstreamMethod(payload.Methods, visibleType)
		if !ok || (limit.Available != nil && !*limit.Available) {
			return
		}
		available[visibleType] = true
		methods = append(methods, UpstreamRechargeMethod{Type: visibleType, Name: name})
	}
	add("alipay", "支付宝")
	add("wxpay", "微信支付")

	plans := make([]UpstreamSubscriptionPlan, 0, len(payload.Plans))
	for _, plan := range payload.Plans {
		if plan.ID <= 0 {
			continue
		}
		paymentMethods := make([]string, 0, len(methods))
		for _, method := range methods {
			if available[method.Type] {
				paymentMethods = append(paymentMethods, method.Type)
			}
		}
		plans = append(plans, UpstreamSubscriptionPlan{
			ID:              strconv.FormatInt(plan.ID, 10),
			Name:            strings.TrimSpace(plan.Name),
			Description:     strings.TrimSpace(plan.Description),
			Price:           plan.Price,
			Currency:        "CNY",
			Validity:        formatUpstreamValidity(plan.ValidityDays, plan.ValidityUnit),
			GroupName:       strings.TrimSpace(plan.GroupName),
			DailyLimitUSD:   plan.DailyLimitUSD,
			WeeklyLimitUSD:  plan.WeeklyLimitUSD,
			MonthlyLimitUSD: plan.MonthlyLimitUSD,
			Features:        safeUpstreamStrings(plan.Features),
			PaymentMethods:  paymentMethods,
		})
	}
	return &UpstreamSubscriptionInfo{Plans: plans, Methods: methods}, nil
}

func formatUpstreamValidity(days int, unit string) string {
	if days <= 0 {
		return ""
	}
	switch strings.TrimSpace(unit) {
	case "month", "months":
		return strconv.Itoa(days) + " 个月"
	case "year", "years":
		return strconv.Itoa(days) + " 年"
	default:
		return strconv.Itoa(days) + " 天"
	}
}

type upstreamOrderPayment struct {
	PayURL      string     `json:"pay_url"`
	QRCode      string     `json:"qr_code"`
	ExpiresAt   *time.Time `json:"expires_at"`
	ResultType  string     `json:"result_type"`
	PaymentMode string     `json:"payment_mode"`
}

// CreateRecharge creates a balance order upstream.
func (c *UpstreamChannelClient) CreateRecharge(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, req UpstreamRechargeRequest) (*UpstreamPaymentLaunch, error) {
	method := strings.TrimSpace(req.PaymentMethod)
	if method != "alipay" && method != "wxpay" {
		return nil, errors.New("payment method must be alipay or wxpay")
	}
	if req.Amount <= 0 {
		return nil, errors.New("amount must be greater than zero")
	}
	return c.createOrder(ctx, siteURL, proxyURL, session, map[string]any{
		"amount":         mathRound2(req.Amount),
		"payment_type":   method,
		"order_type":     "balance",
		"is_mobile":      req.IsMobile,
		"payment_source": "hosted_redirect",
	})
}

// CreateSubscription creates a subscription order upstream.
func (c *UpstreamChannelClient) CreateSubscription(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, req UpstreamSubscriptionRequest) (*UpstreamPaymentLaunch, error) {
	planID, err := strconv.ParseInt(strings.TrimSpace(req.PlanID), 10, 64)
	if err != nil || planID <= 0 {
		return nil, errors.New("plan id is invalid")
	}
	method := strings.TrimSpace(req.PaymentMethod)
	if method != "alipay" && method != "wxpay" {
		return nil, errors.New("payment method must be alipay or wxpay")
	}
	return c.createOrder(ctx, siteURL, proxyURL, session, map[string]any{
		"payment_type":   method,
		"order_type":     "subscription",
		"plan_id":        planID,
		"is_mobile":      req.IsMobile,
		"payment_source": "hosted_redirect",
	})
}

func (c *UpstreamChannelClient) createOrder(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, payload map[string]any) (*UpstreamPaymentLaunch, error) {
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodPost, "/api/v1/payment/orders", session, payload)
	if err != nil {
		return nil, err
	}
	var order upstreamOrderPayment
	if err := decodeUpstreamChannelJSON(body, &order); err != nil {
		return nil, err
	}
	if strings.TrimSpace(order.PayURL) == "" && strings.TrimSpace(order.QRCode) == "" {
		return nil, errors.New("upstream returned no payment url")
	}
	return &UpstreamPaymentLaunch{
		PayURL:      order.PayURL,
		QRCode:      order.QRCode,
		ExpiresAt:   order.ExpiresAt,
		ResultType:  order.ResultType,
		PaymentMode: order.PaymentMode,
	}, nil
}

// SubscriptionUsage lists the account's active subscriptions and quota windows.
func (c *UpstreamChannelClient) SubscriptionUsage(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession) ([]UpstreamSubscriptionUsage, error) {
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodGet, "/api/v1/subscriptions/progress", session, nil)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return nil, err
	}
	out := make([]UpstreamSubscriptionUsage, 0, len(raw))
	for _, item := range raw {
		usage := decodeUpstreamSubscription(item)
		if usage.ID <= 0 {
			continue
		}
		out = append(out, usage)
	}
	return out, nil
}

type upstreamUsageWindowWire struct {
	LimitUSD     float64    `json:"limit_usd"`
	UsedUSD      float64    `json:"used_usd"`
	RemainingUSD float64    `json:"remaining_usd"`
	Percentage   float64    `json:"percentage"`
	ResetsAt     *time.Time `json:"resets_at"`
}

type upstreamSubscriptionWire struct {
	ID            int64                    `json:"id"`
	GroupName     string                   `json:"group_name"`
	Status        string                   `json:"status"`
	ExpiresAt     *time.Time               `json:"expires_at"`
	ExpiresInDays int                      `json:"expires_in_days"`
	Daily         *upstreamUsageWindowWire `json:"daily"`
	Weekly        *upstreamUsageWindowWire `json:"weekly"`
	Monthly       *upstreamUsageWindowWire `json:"monthly"`
	Subscription  *struct {
		ID        int64      `json:"id"`
		Status    string     `json:"status"`
		ExpiresAt *time.Time `json:"expires_at"`
		Group     *struct {
			Name string `json:"name"`
		} `json:"group"`
	} `json:"subscription"`
	Progress *struct {
		GroupName     string                   `json:"group_name"`
		ExpiresAt     *time.Time               `json:"expires_at"`
		ExpiresInDays int                      `json:"expires_in_days"`
		Daily         *upstreamUsageWindowWire `json:"daily"`
		Weekly        *upstreamUsageWindowWire `json:"weekly"`
		Monthly       *upstreamUsageWindowWire `json:"monthly"`
	} `json:"progress"`
}

func decodeUpstreamSubscription(item map[string]any) UpstreamSubscriptionUsage {
	encoded, _ := json.Marshal(item)
	var raw upstreamSubscriptionWire
	_ = json.Unmarshal(encoded, &raw)
	out := UpstreamSubscriptionUsage{
		ID: raw.ID, GroupName: raw.GroupName, Status: raw.Status,
		ExpiresAt: raw.ExpiresAt, ExpiresInDays: raw.ExpiresInDays,
	}
	if raw.Progress != nil {
		if raw.Progress.GroupName != "" {
			out.GroupName = raw.Progress.GroupName
		}
		if raw.Progress.ExpiresAt != nil {
			out.ExpiresAt = raw.Progress.ExpiresAt
		}
		if raw.Progress.ExpiresInDays > 0 {
			out.ExpiresInDays = raw.Progress.ExpiresInDays
		}
	}
	if raw.Subscription != nil {
		if out.ID <= 0 {
			out.ID = raw.Subscription.ID
		}
		if out.GroupName == "" && raw.Subscription.Group != nil {
			out.GroupName = raw.Subscription.Group.Name
		}
		if out.ExpiresAt == nil {
			out.ExpiresAt = raw.Subscription.ExpiresAt
		}
		if out.Status == "" {
			out.Status = raw.Subscription.Status
		}
	}
	daily, weekly, monthly := raw.Daily, raw.Weekly, raw.Monthly
	if raw.Progress != nil {
		if daily == nil {
			daily = raw.Progress.Daily
		}
		if weekly == nil {
			weekly = raw.Progress.Weekly
		}
		if monthly == nil {
			monthly = raw.Progress.Monthly
		}
	}
	out.Daily = upstreamUsageWindow(daily)
	out.Weekly = upstreamUsageWindow(weekly)
	out.Monthly = upstreamUsageWindow(monthly)
	return out
}

func upstreamUsageWindow(window *upstreamUsageWindowWire) *UpstreamSubscriptionUsageWindow {
	if window == nil {
		return nil
	}
	limit, used, remaining, percentage := window.LimitUSD, window.UsedUSD, window.RemainingUSD, window.Percentage
	if limit <= 0 && used <= 0 && remaining <= 0 {
		return nil
	}
	if remaining == 0 && limit > 0 {
		remaining = limit - used
		if remaining < 0 {
			remaining = 0
		}
	}
	if percentage == 0 && limit > 0 {
		percentage = used / limit * 100
	}
	return &UpstreamSubscriptionUsageWindow{
		LimitUSD: limit, UsedUSD: used, RemainingUSD: remaining, Percentage: percentage, ResetsAt: window.ResetsAt,
	}
}

// Redeem applies one redeem code upstream.
func (c *UpstreamChannelClient) Redeem(ctx context.Context, siteURL, proxyURL string, session *UpstreamChannelSession, code string) (*UpstreamRedeemResult, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return nil, errors.New("redeem code is required")
	}
	body, err := c.doJSON(ctx, siteURL, proxyURL, http.MethodPost, "/api/v1/redeem", session, map[string]string{"code": trimmed})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Message        string   `json:"message"`
		Type           string   `json:"type"`
		Value          float64  `json:"value"`
		NewBalance     *float64 `json:"new_balance"`
		NewConcurrency *int     `json:"new_concurrency"`
		GroupName      *string  `json:"group_name"`
		ValidityDays   *int     `json:"validity_days"`
		Group          *struct {
			Name string `json:"name"`
		} `json:"group"`
	}
	if err := decodeUpstreamChannelJSON(body, &raw); err != nil {
		return nil, err
	}
	out := &UpstreamRedeemResult{
		Message: raw.Message, Type: raw.Type, Value: raw.Value,
		NewBalance: raw.NewBalance, NewConcurrency: raw.NewConcurrency,
		GroupName: raw.GroupName, ValidityDays: raw.ValidityDays,
	}
	if out.GroupName == nil && raw.Group != nil && raw.Group.Name != "" {
		name := raw.Group.Name
		out.GroupName = &name
	}
	if out.Type == "" {
		switch {
		case raw.Group != nil && raw.ValidityDays != nil:
			out.Type = "subscription"
		case raw.NewConcurrency != nil:
			out.Type = "concurrency"
		default:
			out.Type = "balance"
		}
	}
	if strings.TrimSpace(out.Message) == "" {
		out.Message = "redeem succeeded"
	}
	return out, nil
}

// mathRound2 rounds a money amount to two decimals without importing math
// just for one call site.
func mathRound2(value float64) float64 {
	if value >= 0 {
		return float64(int64(value*100+0.5)) / 100
	}
	return float64(int64(value*100-0.5)) / 100
}
