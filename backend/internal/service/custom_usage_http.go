package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// The shared billing probe validates DNS before transport acquisition, but its
// dialer resolves again and permits configured proxies. This small direct-only
// adapter pins validated addresses at EACH dial, without altering gateway I/O.
type customUsageNetwork struct {
	lookup func(context.Context, string) ([]net.IPAddr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func newCustomUsageClient() *http.Client {
	n := customUsageNetwork{lookup: net.DefaultResolver.LookupIPAddr, dial: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{
		Proxy: nil, DialContext: n.dialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
		MaxResponseHeaderBytes: 16 * 1024, DisableKeepAlives: true, DisableCompression: true,
	}}
}

var customUsageBlockedPrefixes = func() []netip.Prefix {
	ranges := []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "168.63.129.16/32", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"}
	out := make([]netip.Prefix, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, netip.MustParsePrefix(r))
	}
	return out
}()

func customUsagePublicIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range customUsageBlockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func (n customUsageNetwork) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid_destination")
	}
	ips, err := n.lookup(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("dns_failed")
	}
	for _, ip := range ips {
		a, ok := netip.AddrFromSlice(ip.IP)
		if !ok || ip.Zone != "" || !customUsagePublicIP(a) {
			return nil, errors.New("blocked_destination")
		}
	}
	// Never pass the hostname to the dialer after validation (DNS rebinding).
	for _, ip := range ips {
		conn, err := n.dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.New("connection_failed")
}
func customUsageValidateURL(raw string) (*url.URL, error) {
	if strings.ContainsAny(raw, "\r\n\x00\\") || len(raw) > customUsageMaxURLBytes {
		return nil, ErrCustomUsageConfig
	}
	if _, err := urlvalidator.ValidateHTTPURL(raw, true, urlvalidator.ValidationOptions{}); err != nil {
		return nil, ErrCustomUsageConfig
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" || strings.Contains(u.Hostname(), "%") {
		return nil, ErrCustomUsageConfig
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if !customUsagePublicIP(ip) {
			return nil, ErrCustomUsageConfig
		}
	} else {
		if !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
			return nil, ErrCustomUsageConfig
		}
		numeric := true
		for _, r := range host {
			if (r < '0' || r > '9') && r != '.' {
				numeric = false
			}
		}
		if numeric {
			return nil, ErrCustomUsageConfig
		}
	}
	return u, nil
}

type customUsagePrepared struct {
	config    CustomUsageConfig
	request   *http.Request
	sensitive []string
}

func prepareCustomUsage(a *Account, c CustomUsageConfig, secrets customUsageSecrets) (customUsagePrepared, error) {
	var out customUsagePrepared
	if a == nil || a.Type != AccountTypeAPIKey || a.IsCredentialShadow() {
		return out, ErrUpstreamBillingProbeAccountInvalid
	}
	if err := normalizeCustomUsage(&c); err != nil {
		return out, err
	}
	out.config = c
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(a.GetCredential("base_url")), "/")
	}
	if base != "" {
		u, err := customUsageValidateURL(base)
		if err != nil || u.RawQuery != "" {
			return out, ErrCustomUsageConfig
		}
	}
	apiKey := secrets.APIKey
	if apiKey == "" {
		apiKey = a.GetCredential("api_key")
	}
	access := secrets.AccessToken
	if access == "" {
		access = a.GetCredential("access_token")
	}
	user := c.UserID
	if user == "" {
		user = a.GetCredential("user_id")
	}
	vars := map[string]string{"baseUrl": base, "apiKey": apiKey, "accessToken": access, "userId": user}
	for _, v := range vars {
		if len(v) > 8192 || strings.ContainsAny(v, "\r\n\x00") {
			return out, ErrCustomUsageConfig
		}
	}
	public, err := json.Marshal(publicCustomUsage(c))
	if err != nil {
		return out, ErrCustomUsageConfig
	}
	for _, secret := range []string{apiKey, access} {
		if secret != "" && strings.Contains(string(public), secret) {
			return out, ErrCustomUsageConfig
		}
	}
	if !c.Enabled {
		return out, nil
	}
	raw := c.Request.URL
	// Reuse the billing probe's endpoint joiner for the two official presets.
	if raw == "{{baseUrl}}/v1/usage" && c.Template != "custom" {
		raw = buildOpenAIEndpointURL(base, "/v1/usage")
	}
	if c.Template == "newapi" && raw == "{{baseUrl}}/api/user/self" {
		raw = strings.TrimSuffix(base, "/v1") + "/api/user/self"
	}
	qmark := strings.Index(raw, "?")
	prefix := raw
	if qmark >= 0 {
		prefix = raw[:qmark]
	}
	for _, k := range []string{"apiKey", "accessToken", "userId"} {
		if strings.Contains(prefix, "{{"+k+"}}") {
			return out, ErrCustomUsageConfig
		}
	}
	expanded, err := customUsageExpand(raw, vars, true, customUsageMaxURLBytes)
	if err != nil {
		return out, err
	}
	u, err := customUsageValidateURL(expanded)
	if err != nil {
		return out, err
	}
	// Literal credentials are not allowed in host/path/base URL, only headers/query.
	for _, v := range []string{apiKey, access} {
		if v != "" && (strings.Contains(u.Host+u.Path, v) || strings.Contains(base, v)) {
			return out, ErrCustomUsageConfig
		}
	}
	headerBytes := len("Accept: application/json\r\nHost: \r\nUser-Agent: Go-http-client/1.1\r\nConnection: close\r\n\r\n") + len(u.Host)
	for name, template := range c.Request.Headers {
		length, err := customUsageExpandedLength(template, vars, false, customUsageMaxHeaderValueBytes)
		if err != nil {
			return out, err
		}
		headerBytes += len(name) + length + 4
		if headerBytes > customUsageMaxHeaderBytes {
			return out, ErrCustomUsageConfig
		}
	}
	requestURL := u.String()
	if len(requestURL) > customUsageMaxURLBytes {
		return out, ErrCustomUsageConfig
	}
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return out, ErrCustomUsageConfig
	}
	req.Header.Set("Accept", "application/json")
	out.sensitive = []string{apiKey, access}
	for _, vs := range u.Query() {
		out.sensitive = append(out.sensitive, vs...)
	}
	for k, v := range c.Request.Headers {
		value, err := customUsageExpand(v, vars, false, customUsageMaxHeaderValueBytes)
		if err != nil || strings.ContainsAny(value, "\r\n\x00") {
			return out, ErrCustomUsageConfig
		}
		req.Header.Set(k, value)
		if strings.EqualFold(k, "Cookie") && value != "" {
			cookies, err := http.ParseCookie(value)
			if err != nil {
				return out, ErrCustomUsageConfig
			}
			for _, cookie := range cookies {
				if cookie.Value != "" {
					out.sensitive = append(out.sensitive, cookie.Value)
				}
			}
		}
		if !strings.EqualFold(k, "Accept") && value != "" {
			out.sensitive = append(out.sensitive, value)
			if parts := strings.Fields(value); len(parts) == 2 && (strings.EqualFold(parts[0], "Bearer") || strings.EqualFold(parts[0], "Basic")) {
				out.sensitive = append(out.sensitive, parts[1])
			}
		}
	}
	out.request = req
	return out, nil
}

func parseCustomUsage(body []byte, c CustomUsageConfig, sensitive []string) (CustomUsageResult, error) {
	var result CustomUsageResult
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return result, errCustomUsageResponse
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return result, errCustomUsageResponse
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return result, errCustomUsageResponse
	}
	for _, k := range []string{"success", "isValid", "code"} {
		if b, ok := obj[k].(bool); ok && !b {
			return result, errCustomUsageResponse
		}
	}
	if code, ok := obj["code"]; ok {
		switch value := code.(type) {
		case json.Number:
			if value.String() != "0" && value.String() != "200" {
				return result, errCustomUsageResponse
			}
		case string:
			if value != "0" && value != "200" && value != "ok" && value != "success" {
				return result, errCustomUsageResponse
			}
		}
	}
	if status, ok := obj["status"].(string); ok && (status == "error" || status == "failed" || status == "failure") {
		return result, errCustomUsageResponse
	}
	if value, exists := obj["error"]; exists && value != nil {
		if text, ok := value.(string); !ok || text != "" {
			return result, errCustomUsageResponse
		}
	}
	if c.Template == "newapi" {
		if success, present := obj["success"]; present && success != true {
			return result, errCustomUsageResponse
		}
	}
	// Custom token-mode mappings retain NewAPI's unlimited marker semantics.
	if unlimited, _ := customUsageLookup(root, "data.unlimited_quota"); unlimited == true {
		return result, errors.New("unlimited_quota")
	}
	targets := []struct {
		spec *CustomUsageNumber
		dest **float64
	}{{c.Extractor.Remaining, &result.Remaining}, {c.Extractor.Used, &result.Used}, {c.Extractor.Total, &result.Total}}
	for _, target := range targets {
		if target.spec == nil {
			continue
		}
		v, ok := customUsageLookup(root, target.spec.Path)
		if !ok {
			return CustomUsageResult{}, errCustomUsageResponse
		}
		var value float64
		var err error
		switch n := v.(type) {
		case json.Number:
			value, err = n.Float64()
		case string:
			value, err = strconv.ParseFloat(n, 64)
		default:
			return CustomUsageResult{}, errCustomUsageResponse
		}
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return CustomUsageResult{}, errCustomUsageResponse
		}
		if target.spec.Divisor != nil {
			value /= *target.spec.Divisor
		}
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return CustomUsageResult{}, errCustomUsageResponse
		}
		*target.dest = &value
	}
	if result.Remaining == nil && result.Used == nil && result.Total == nil {
		return result, errCustomUsageResponse
	}
	for _, target := range []struct {
		spec *CustomUsageText
		dest *string
		max  int
	}{{c.Extractor.Unit, &result.Unit, 24}, {c.Extractor.PlanName, &result.PlanName, 128}} {
		if target.spec == nil {
			continue
		}
		value := target.spec.Value
		if target.spec.Path != "" {
			v, exists := customUsageLookup(root, target.spec.Path)
			if !exists {
				continue
			}
			var ok bool
			value, ok = v.(string)
			if !ok {
				return CustomUsageResult{}, errCustomUsageResponse
			}
		}
		if !customUsageSafeText(value, target.max) {
			return CustomUsageResult{}, errCustomUsageResponse
		}
		for _, secret := range sensitive {
			if secret != "" && strings.Contains(value, secret) {
				return CustomUsageResult{}, errCustomUsageResponse
			}
		}
		*target.dest = value
	}
	if c.Template == "sub2api" && result.Unit == "" {
		result.Unit = "USD"
	}
	return result, nil
}

func (s *CustomUsageService) fetch(ctx context.Context, p customUsagePrepared) CustomUsageResult {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(p.config.TimeoutSeconds)*time.Second)
	defer cancel()
	result := customUsageEmpty(p.config, true)
	req := p.request.WithContext(WithHTTPUpstreamPublicHostsOnly(WithHTTPUpstreamRedirectsDisabled(ctx)))
	resp, err := s.client.Do(req)
	if err != nil {
		result.Error = "request_failed"
		return result
	}
	if resp == nil || resp.Body == nil {
		result.Error = "invalid_response"
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		result.Error = "redirect_blocked"
		return result
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = "upstream_error"
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamBillingProbeMaxBodyBytes+1))
	if err != nil {
		result.Error = "request_failed"
		return result
	}
	if len(body) > upstreamBillingProbeMaxBodyBytes {
		result.Error = "response_too_large"
		return result
	}
	parsed, err := parseCustomUsage(body, p.config, p.sensitive)
	if err != nil {
		result.Error = "invalid_response"
		if err.Error() == "unlimited_quota" {
			result.Error = "unlimited_quota"
		}
		return result
	}
	parsed.Enabled = true
	parsed.Configured = true
	parsed.IntervalMinutes = p.config.IntervalMinutes
	now := s.now().UTC()
	parsed.UpdatedAt = &now
	return parsed
}
