package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const chanshuiMaxBody = 2 << 20

var chanshuiTokenPattern = regexp.MustCompile(`[A-Za-z0-9_./+=:-]{8,}`)
var chanshuiKeyPattern = regexp.MustCompile(`(?i)(?:sk-|Bearer\s+)[A-Za-z0-9_./+=:-]+`)

func chanshuiFingerprint(secret string) string {
	s := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(s[:])
}

func chanshuiRetryAfter(value string) time.Duration {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 1 {
		return time.Minute
	}
	if n > int64((time.Duration(1<<63-1))/time.Second) {
		n = int64((time.Duration(1<<63 - 1)) / time.Second)
	}
	return time.Duration(n) * time.Second
}

func chanshuiPollURL(base, poll, id string) (string, error) {
	origin, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid audit service URL")
	}
	rel, err := url.Parse(poll)
	if err != nil {
		return "", fmt.Errorf("invalid audit poll URL")
	}
	u := origin.ResolveReference(rel)
	expected := strings.TrimRight(origin.Path, "/") + "/api/v1/audits/" + id
	if id == "" || strings.ContainsAny(id, "/?#\\\r\n") || u.Scheme != origin.Scheme || u.Host != origin.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != expected {
		return "", fmt.Errorf("audit poll URL does not match the configured service and audit id")
	}
	return u.String(), nil
}

// Keep unknown contract fields without persisting credentials that a remote
// service might echo. The original secret fingerprint also survives rotation.
func redactChanshui(value any, secret, fingerprint string) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "api_key") || strings.Contains(lower, "authorization") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || lower == "access_token" {
				out[k] = "[REDACTED]"
			} else {
				out[k] = redactChanshui(x, secret, fingerprint)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = redactChanshui(x, secret, fingerprint)
		}
		return out
	case string:
		if secret != "" {
			v = strings.ReplaceAll(v, secret, "[REDACTED]")
		}
		v = chanshuiTokenPattern.ReplaceAllStringFunc(v, func(token string) string {
			if fingerprint != "" && chanshuiFingerprint(token) == fingerprint {
				return "[REDACTED]"
			}
			return token
		})
		return chanshuiKeyPattern.ReplaceAllString(v, "[REDACTED]")
	default:
		return value
	}
}

func chanshuiExchange(ctx context.Context, client *http.Client, method, target string, body []byte) (int, http.Header, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("cannot prepare audit request")
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	// No Authorization header, cookies, or operation token.
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("audit service connection failed")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, chanshuiMaxBody+1))
	if err != nil || len(raw) > chanshuiMaxBody {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("audit response unavailable or exceeds size limit")
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("audit service returned invalid JSON")
	}
	return resp.StatusCode, resp.Header, data, nil
}

func chanshuiUpstream(account *Account, config ChanshuiConfig) (string, string, string, error) {
	if account == nil || account.Type != AccountTypeAPIKey || account.IsCredentialShadow() {
		return "", "", "", fmt.Errorf("chanshui requires a directly configured API-key account")
	}
	key := strings.TrimSpace(account.GetCredential("api_key"))
	if key == "" {
		return "", "", "", fmt.Errorf("account has no upstream API key")
	}
	protocol := config.Protocol
	base := strings.TrimSpace(account.GetCredential("base_url"))
	switch {
	case account.IsAdaptiveAPIProtocol():
		selected := APIProtocolChatCompletions
		switch protocol {
		case "anthropic":
			selected = APIProtocolAnthropic
		case "openai":
			selected = APIProtocolResponses
		}
		base = account.GetCNProtocolBaseURL(selected)
	case account.IsAnthropicProtocol():
		base = account.GetAnthropicProtocolBaseURL()
	case account.Platform == PlatformAnthropic:
		base = account.GetBaseURL()
	case account.Platform == PlatformGrok:
		base = account.GetGrokBaseURL()
	case base == "":
		base = account.GetOpenAIBaseURL()
	}
	u, err := customUsageValidateURL(base)
	if err != nil || u.RawQuery != "" {
		return "", "", "", fmt.Errorf("account needs a public upstream base URL")
	}
	return base, key, protocol, nil
}
