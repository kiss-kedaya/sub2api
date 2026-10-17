package chanshui

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

// MaxBody bounds audit-service response parsing.
const MaxBody = 2 << 20

var tokenPattern = regexp.MustCompile(`[A-Za-z0-9_./+=:-]{8,}`)
var keyPattern = regexp.MustCompile(`(?i)(?:sk-|Bearer\s+)[A-Za-z0-9_./+=:-]+`)

// LooksLikeKey reports whether a string carries an API key or bearer token
// shape; used to keep such values out of user-facing audit summaries.
func LooksLikeKey(s string) bool {
	return keyPattern.MatchString(s)
}

// Fingerprint is the hex SHA-256 of a secret; used to keep evidence searchable
// across credential rotation without persisting the secret itself.
func Fingerprint(secret string) string {
	s := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(s[:])
}

// RetryAfter parses a bounded Retry-After header value in seconds.
func RetryAfter(value string) time.Duration {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || n < 1 {
		return time.Minute
	}
	if n > int64((time.Duration(1<<63-1))/time.Second) {
		n = int64((time.Duration(1<<63 - 1)) / time.Second)
	}
	return time.Duration(n) * time.Second
}

// PollURL validates that a poll target stays on the configured service origin
// and matches the audit id; remote responses must not redirect polling.
func PollURL(base, poll, id string) (string, error) {
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

// Redact keeps unknown contract fields while stripping credentials a remote
// service might echo. The original secret fingerprint also survives rotation.
func Redact(value any, secret, fingerprint string) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "api_key") || strings.Contains(lower, "authorization") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || lower == "access_token" {
				out[k] = "[REDACTED]"
			} else {
				out[k] = Redact(x, secret, fingerprint)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = Redact(x, secret, fingerprint)
		}
		return out
	case string:
		if secret != "" {
			v = strings.ReplaceAll(v, secret, "[REDACTED]")
		}
		v = tokenPattern.ReplaceAllStringFunc(v, func(token string) string {
			if fingerprint != "" && Fingerprint(token) == fingerprint {
				return "[REDACTED]"
			}
			return token
		})
		return keyPattern.ReplaceAllString(v, "[REDACTED]")
	default:
		return value
	}
}

// Exchange performs one audit-service HTTP call. No Authorization header,
// cookies, or operation token: the audit service authenticates by base_url.
func Exchange(ctx context.Context, client *http.Client, method, target string, body []byte) (int, http.Header, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("cannot prepare audit request")
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("audit service connection failed")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil || len(raw) > MaxBody {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("audit response unavailable or exceeds size limit")
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("audit service returned invalid JSON")
	}
	return resp.StatusCode, resp.Header, data, nil
}

// State is the persisted per-plan audit progress. It contains no upstream
// credential or request body: a worker restart resumes GET polling.
type State struct {
	ID                 string    `json:"id"`
	PollURL            string    `json:"poll_url"`
	ServiceURL         string    `json:"service_url"`
	StartedAt          time.Time `json:"started_at"`
	ExpiresAt          int64     `json:"expires_at"`
	KeyFingerprint     string    `json:"key_fingerprint"`
	Status             string    `json:"status"`
	Skipped            any       `json:"skipped,omitempty"`
	DecisionConfigHash string    `json:"decision_config_hash,omitempty"`
}
