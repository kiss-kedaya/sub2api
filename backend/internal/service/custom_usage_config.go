package service

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// CustomUsageExtraKey stores only a redacted, declarative configuration.
	CustomUsageExtraKey = "custom_usage_config"
	// CustomUsageCredentialsKey is never serialized by ordinary account DTOs.
	CustomUsageCredentialsKey      = "custom_usage_secrets"
	CustomUsageMaxBatchSize        = 50
	customUsageMaxURLBytes         = 8192
	customUsageMaxHeaderValueBytes = 8192
	customUsageMaxHeaderBytes      = 32 * 1024
	customUsageMaxHeaderNameBytes  = 128
)

var ErrCustomUsageConfig = infraerrors.BadRequest("INVALID_CUSTOM_USAGE_CONFIG", "invalid custom usage configuration")
var errCustomUsageResponse = errors.New("invalid_response")

// CustomUsageNumber selects a finite, nonnegative JSON number; divisor defaults to one.
type CustomUsageNumber struct {
	Path    string   `json:"path"`
	Divisor *float64 `json:"divisor,omitempty"`
}

// CustomUsageText selects a short string or a fixed literal.
type CustomUsageText struct {
	Path  string `json:"path,omitempty"`
	Value string `json:"value,omitempty"`
}

// CustomUsageExtractor deliberately has no executable expressions.
type CustomUsageExtractor struct {
	Remaining *CustomUsageNumber `json:"remaining,omitempty"`
	Used      *CustomUsageNumber `json:"used,omitempty"`
	Total     *CustomUsageNumber `json:"total,omitempty"`
	Unit      *CustomUsageText   `json:"unit,omitempty"`
	PlanName  *CustomUsageText   `json:"plan_name,omitempty"`
}

// CustomUsageRequest describes a single credential-bearing GET request.
type CustomUsageRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

// CustomUsageConfig is the admin input. Empty secret values preserve previous values.
type CustomUsageConfig struct {
	Enabled          bool                 `json:"enabled"`
	Template         string               `json:"template"`
	BaseURL          string               `json:"base_url"`
	APIKey           string               `json:"api_key,omitempty"`
	AccessToken      string               `json:"access_token,omitempty"`
	UserID           string               `json:"user_id,omitempty"`
	TimeoutSeconds   int                  `json:"timeout_seconds"`
	IntervalMinutes  int                  `json:"interval_minutes"`
	Request          CustomUsageRequest   `json:"request"`
	Extractor        CustomUsageExtractor `json:"extractor"`
	ClearAPIKey      bool                 `json:"clear_api_key,omitempty"`
	ClearAccessToken bool                 `json:"clear_access_token,omitempty"`
}

// CustomUsageConfigView has the same editable shape but never includes stored secrets.
type CustomUsageConfigView struct {
	CustomUsageConfig
	Configured             bool `json:"configured"`
	HasAPIKey              bool `json:"has_api_key"`
	HasAccessToken         bool `json:"has_access_token"`
	UsesAccountAPIKey      bool `json:"uses_account_api_key"`
	UsesAccountAccessToken bool `json:"uses_account_access_token"`
}

type customUsageSecrets struct {
	APIKey      string            `json:"api_key,omitempty"`
	AccessToken string            `json:"access_token,omitempty"`
	RequestURL  string            `json:"request_url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

func customUsageDefaults() CustomUsageConfig {
	return CustomUsageConfig{Template: "custom", TimeoutSeconds: 10, Request: CustomUsageRequest{Method: http.MethodGet, Headers: map[string]string{}}}
}

func customUsageAccountDefaults(a *Account) (CustomUsageConfig, bool) {
	c := customUsageDefaults()
	if a == nil || a.Type != AccountTypeAPIKey || a.IsCredentialShadow() {
		return c, false
	}
	base := strings.TrimRight(strings.TrimSpace(a.GetCredential("base_url")), "/")
	if base == "" {
		return c, false
	}
	return CustomUsageConfig{
		Enabled:         true,
		Template:        "general",
		BaseURL:         base,
		TimeoutSeconds:  10,
		IntervalMinutes: 10,
		Request: CustomUsageRequest{
			URL:     "{{baseUrl}}/v1/usage",
			Method:  http.MethodGet,
			Headers: map[string]string{"Authorization": "Bearer {{apiKey}}"},
		},
		Extractor: CustomUsageExtractor{
			Remaining: &CustomUsageNumber{Path: "remaining"},
			Unit:      &CustomUsageText{Value: "USD"},
		},
	}, true
}

func readCustomUsage(a *Account) (CustomUsageConfig, customUsageSecrets, bool) {
	c, configured := customUsageAccountDefaults(a)
	var secrets customUsageSecrets
	var raw any
	explicitlyConfigured := false
	if a != nil {
		raw, explicitlyConfigured = a.Extra[CustomUsageExtraKey]
	}
	if explicitlyConfigured {
		configured = true
		c = customUsageDefaults()
		b, err := json.Marshal(raw)
		if err != nil || json.Unmarshal(b, &c) != nil {
			c = customUsageDefaults()
			c.Enabled = true
			c.TimeoutSeconds = -1
		}
	}
	if a != nil && explicitlyConfigured {
		if b, err := json.Marshal(a.Credentials[CustomUsageCredentialsKey]); err == nil {
			_ = json.Unmarshal(b, &secrets)
		}
	}
	if secrets.RequestURL != "" {
		c.Request.URL = secrets.RequestURL
	}
	if secrets.Headers != nil {
		c.Request.Headers = secrets.Headers
	}
	return c, secrets, configured
}

func mergeCustomUsage(c CustomUsageConfig, old CustomUsageConfig, secrets customUsageSecrets) (CustomUsageConfig, customUsageSecrets) {
	if c.ClearAPIKey {
		secrets.APIKey = ""
	} else if c.APIKey != "" {
		secrets.APIKey = c.APIKey
	}
	if c.ClearAccessToken {
		secrets.AccessToken = ""
	} else if c.AccessToken != "" {
		secrets.AccessToken = c.AccessToken
	}
	if c.Request.URL == "" && c.Template == old.Template {
		c.Request.URL = old.Request.URL
	}
	// A redacted URL read back from GET keeps the original query values.
	if c.Request.URL == customUsagePublicURL(old.Request.URL) {
		c.Request.URL = old.Request.URL
	}
	headers := make(map[string]string, len(c.Request.Headers))
	for k, v := range c.Request.Headers {
		if v == "" {
			v = old.Request.Headers[k]
		}
		headers[k] = v
	}
	c.Request.Headers = headers
	if c.Template != old.Template {
		// Different presets must not reuse an old private request accidentally.
		if c.Request.URL == old.Request.URL {
			c.Request.URL = ""
		}
	}
	c.APIKey = ""
	c.AccessToken = ""
	c.ClearAPIKey = false
	c.ClearAccessToken = false
	return c, secrets
}

func normalizeCustomUsage(c *CustomUsageConfig) error {
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 30 || c.IntervalMinutes < 0 || (c.IntervalMinutes > 0 && c.IntervalMinutes < 5) {
		return ErrCustomUsageConfig
	}
	if c.Template == "" {
		c.Template = "custom"
	}
	switch c.Template {
	case "custom", "general", "newapi", "sub2api":
	default:
		return ErrCustomUsageConfig
	}
	if c.Request.Method == "" {
		c.Request.Method = http.MethodGet
	}
	if c.Request.Method != http.MethodGet {
		return ErrCustomUsageConfig
	}
	if len(c.BaseURL) > 2048 || !customUsageSafeText(c.UserID, 128) || len(c.Request.URL) > 4096 || len(c.Request.Headers) > 24 {
		return ErrCustomUsageConfig
	}
	if c.Request.Headers == nil {
		c.Request.Headers = map[string]string{}
	}
	if c.Template != "custom" && c.Request.URL == "" {
		c.Request.URL = "{{baseUrl}}/v1/usage"
		if c.Template == "newapi" {
			c.Request.URL = "{{baseUrl}}/api/user/self"
		}
	}
	if c.Template != "custom" && len(c.Request.Headers) == 0 {
		c.Request.Headers["Authorization"] = "Bearer {{apiKey}}"
		if c.Template == "newapi" {
			c.Request.Headers["Authorization"] = "Bearer {{accessToken}}"
			c.Request.Headers["New-Api-User"] = "{{userId}}"
		}
	}
	if c.Template != "custom" && c.Extractor.Remaining == nil && c.Extractor.Used == nil && c.Extractor.Total == nil {
		c.Extractor.Remaining = &CustomUsageNumber{Path: "remaining"}
		c.Extractor.Unit = &CustomUsageText{Value: "USD"}
		if c.Template == "sub2api" {
			c.Extractor.Unit = &CustomUsageText{Path: "unit"}
			c.Extractor.PlanName = &CustomUsageText{Path: "planName"}
		}
		if c.Template == "newapi" {
			// QuantumNous/new-api controller/user.go GetSelf: quota + used_quota.
			// common/constants.go defaults QuotaPerUnit to 500000. Custom sites
			// can change that value; the editable divisor is the authority.
			divisor := 500000.0
			c.Extractor.Remaining = &CustomUsageNumber{Path: "data.quota", Divisor: &divisor}
			c.Extractor.Used = &CustomUsageNumber{Path: "data.used_quota", Divisor: &divisor}
			c.Extractor.Unit = &CustomUsageText{Value: "USD"}
		}
	}
	if c.Enabled && (c.Request.URL == "" || (c.Extractor.Remaining == nil && c.Extractor.Used == nil && c.Extractor.Total == nil)) {
		return ErrCustomUsageConfig
	}
	for _, n := range []*CustomUsageNumber{c.Extractor.Remaining, c.Extractor.Used, c.Extractor.Total} {
		if n == nil {
			continue
		}
		if _, err := customUsagePath(n.Path); err != nil {
			return ErrCustomUsageConfig
		}
		if n.Divisor != nil && (*n.Divisor <= 0 || math.IsNaN(*n.Divisor) || math.IsInf(*n.Divisor, 0)) {
			return ErrCustomUsageConfig
		}
	}
	for _, v := range []*CustomUsageText{c.Extractor.Unit, c.Extractor.PlanName} {
		if v == nil {
			continue
		}
		if len(v.Value) > 128 || (v.Path != "" && v.Value != "") {
			return ErrCustomUsageConfig
		}
		if v.Path != "" {
			if _, err := customUsagePath(v.Path); err != nil {
				return ErrCustomUsageConfig
			}
		}
		if !customUsageSafeText(v.Value, 128) {
			return ErrCustomUsageConfig
		}
	}
	seen := map[string]bool{}
	headerBytes := 0
	for k, v := range c.Request.Headers {
		lower := strings.ToLower(k)
		if len(k) > customUsageMaxHeaderNameBytes || k == "__proto__" || k == "constructor" || k == "prototype" || !customUsageHeaderName.MatchString(k) || seen[lower] || len(v) > customUsageMaxHeaderValueBytes || strings.ContainsAny(v, "\r\n\x00") {
			return ErrCustomUsageConfig
		}
		headerBytes += len(k) + len(v) + 4
		if headerBytes > customUsageMaxHeaderBytes {
			return ErrCustomUsageConfig
		}
		seen[lower] = true
		switch lower {
		case "host", "connection", "content-length", "transfer-encoding", "upgrade", "proxy-authorization", "proxy-connection", "te", "trailer", "accept-encoding":
			return ErrCustomUsageConfig
		}
		if !customUsageKnownTemplates(v) {
			return ErrCustomUsageConfig
		}
	}
	if !customUsageKnownTemplates(c.Request.URL) || strings.ContainsAny(c.UserID, "\r\n\x00") {
		return ErrCustomUsageConfig
	}
	return nil
}

var customUsageHeaderName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
var customUsagePathSyntax = regexp.MustCompile(`^(\$\.)?[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_0-9][A-Za-z0-9_-]*|\[[0-9]+\])*$`)
var customUsagePathPart = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$|^[0-9]+$`)

func customUsageKnownTemplates(s string) bool {
	for _, key := range []string{"baseUrl", "apiKey", "accessToken", "userId"} {
		s = strings.ReplaceAll(s, "{{"+key+"}}", "")
	}
	return !strings.ContainsAny(s, "{}")
}

func customUsageTemplateParts(raw string, variables map[string]string, visit func(string, bool) error) error {
	for {
		start := strings.Index(raw, "{{")
		if start < 0 {
			return visit(raw, false)
		}
		if err := visit(raw[:start], false); err != nil {
			return err
		}
		raw = raw[start+2:]
		end := strings.Index(raw, "}}")
		if end < 0 {
			return ErrCustomUsageConfig
		}
		key := raw[:end]
		value, exists := variables[key]
		if !exists || value == "" {
			return ErrCustomUsageConfig
		}
		if err := visit(value, key != "baseUrl"); err != nil {
			return err
		}
		raw = raw[end+2:]
	}
}

func customUsageExpandedLength(raw string, variables map[string]string, query bool, limit int) (int, error) {
	length := 0
	err := customUsageTemplateParts(raw, variables, func(value string, variable bool) error {
		size := len(value)
		if size > limit-length {
			return ErrCustomUsageConfig
		}
		if query && variable {
			for index := 0; index < len(value); index++ {
				letter := value[index]
				if (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z') || (letter >= '0' && letter <= '9') || strings.ContainsRune("-_.~ ", rune(letter)) {
					continue
				}
				if limit-length-size < 2 {
					return ErrCustomUsageConfig
				}
				size += 2
			}
		}
		length += size
		return nil
	})
	return length, err
}

func customUsageExpand(raw string, variables map[string]string, query bool, limit int) (string, error) {
	length, err := customUsageExpandedLength(raw, variables, query, limit)
	if err != nil {
		return "", err
	}
	var expanded strings.Builder
	expanded.Grow(length)
	err = customUsageTemplateParts(raw, variables, func(value string, variable bool) error {
		if query && variable {
			value = url.QueryEscape(value)
		}
		_, _ = expanded.WriteString(value)
		return nil
	})
	if err != nil || expanded.Len() > limit {
		return "", ErrCustomUsageConfig
	}
	return expanded.String(), nil
}

func customUsagePath(path string) ([]string, error) {
	if len(path) == 0 || len(path) > 256 || !customUsagePathSyntax.MatchString(path) {
		return nil, errCustomUsageResponse
	}
	path = strings.TrimPrefix(path, "$.")
	if strings.HasPrefix(path, "$") {
		return nil, errCustomUsageResponse
	}
	path = strings.ReplaceAll(path, "[", ".")
	path = strings.ReplaceAll(path, "]", "")
	parts := strings.Split(path, ".")
	if len(parts) > 24 {
		return nil, errCustomUsageResponse
	}
	for _, p := range parts {
		if !customUsagePathPart.MatchString(p) || p == "__proto__" || p == "constructor" || p == "prototype" {
			return nil, errCustomUsageResponse
		}
	}
	return parts, nil
}
func customUsageLookup(root any, path string) (any, bool) {
	parts, err := customUsagePath(path)
	if err != nil {
		return nil, false
	}
	current := root
	for _, p := range parts {
		switch v := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = v[p]
			if !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			current = v[i]
		default:
			return nil, false
		}
	}
	return current, true
}
func customUsageSafeText(s string, max int) bool {
	if len(s) > max || strings.ContainsAny(s, "<>{}\\") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func customUsagePublicURL(raw string) string {
	// Never expose literal query credentials, even when their names are vendor-specific.
	u, err := url.Parse(strings.ReplaceAll(raw, "{{baseUrl}}", "https://usage.invalid"))
	if err != nil {
		return ""
	}
	q := u.Query()
	for k, vs := range q {
		for i, v := range vs {
			if v != "{{apiKey}}" && v != "{{accessToken}}" && v != "{{userId}}" {
				vs[i] = ""
			}
		}
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	u.User = nil
	u.Fragment = ""
	out := u.String()
	if strings.HasPrefix(raw, "{{baseUrl}}") {
		out = strings.Replace(out, "https://usage.invalid", "{{baseUrl}}", 1)
	}
	// Keep placeholders editable rather than percent-encoded.
	for _, key := range []string{"apiKey", "accessToken", "userId"} {
		out = strings.ReplaceAll(out, url.QueryEscape("{{"+key+"}}"), "{{"+key+"}}")
	}
	return out
}
func publicCustomUsage(c CustomUsageConfig) CustomUsageConfig {
	c.APIKey = ""
	c.AccessToken = ""
	c.ClearAPIKey = false
	c.ClearAccessToken = false
	c.Request.URL = customUsagePublicURL(c.Request.URL)
	headers := map[string]string{}
	for k, v := range c.Request.Headers {
		switch v {
		case "{{apiKey}}", "Bearer {{apiKey}}", "{{accessToken}}", "Bearer {{accessToken}}", "{{userId}}", "application/json":
			headers[k] = v
		default:
			headers[k] = ""
		}
	}
	c.Request.Headers = headers
	return c
}
