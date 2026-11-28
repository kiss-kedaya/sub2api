package service

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/chanshui"
)

const maxPublicQualityErrorRunes = 240

var (
	publicQualityIPv4Pattern        = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	publicQualityIPv6Pattern        = regexp.MustCompile(`\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b`)
	publicQualityBracketIPv6Pattern = regexp.MustCompile(`\[[0-9a-f:.%]+\]`)
	publicQualityHostPortPattern    = regexp.MustCompile(`\b[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+:\d{1,5}\b`)
	publicQualityHTTPStatusPattern  = regexp.MustCompile(`\bhttp[/ ]?[45]\d\d\b`)
	publicQualityStatusPhraseRegex  = regexp.MustCompile(`\b(?:40[0134]|408|429|50[0234])\s+[a-z]`)
	publicQualityJWTPattern         = regexp.MustCompile(`\beyj[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\b`)
	publicQualityKeyPrefixPattern   = regexp.MustCompile(`\b(?:aiza|xai-|gsk_|ghp_|gho_|akia|ant-api)[a-z0-9_-]{6,}\b`)
	publicQualityEOFPattern         = regexp.MustCompile(`\beof\b`)
)

var publicQualitySensitiveSubstrings = []string{
	"://",
	"::",
	"authorization",
	"bearer ",
	"bearer:",
	"api_key",
	"api key",
	"apikey",
	"x-api-key",
	"access_token",
	"refresh_token",
	"session_token",
	"oauth",
	"jwt",
	"token",
	"password",
	"secret",
	"cookie",
	"account_id",
	"account=",
	"account #",
	"plan_id",
	"group_id",
	"dial tcp",
	"dial udp",
	"read tcp",
	"write tcp",
	"connection refused",
	"connection reset",
	"broken pipe",
	"remote error",
	"no such host",
	"lookup ",
	"tls:",
	"x509:",
	"context deadline",
	"context canceled",
	"i/o timeout",
	"status code",
	"rate limit",
	"rate_limit",
	"insufficient_quota",
	"unauthorized",
	"forbidden",
	"bad gateway",
	"gateway timeout",
	"service unavailable",
	"internal server error",
	"no available accounts",
	"upstream",
	"sql:",
	"postgres",
	"pgconn",
	"redis",
	"panic",
	"goroutine",
	"base_url",
	"endpoint",
	"request_id",
	"trace_id",
	"localhost",
	".internal",
	".local",
	"cloudflare",
	"safeline",
	"nginx",
	"proxy",
}

// SanitizePublicQualityErrorMessage ensures end-user quality events never leak
// internal upstream errors, URLs, IP addresses, account IDs, or credentials.
// Benign short plain-language degradation verdicts are preserved only for
// degraded events; non-degraded statuses always return an empty string.
func SanitizePublicQualityErrorMessage(status, raw string) string {
	if status != "degraded" || strings.TrimSpace(raw) == "" {
		return ""
	}
	for _, r := range raw {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return ""
		}
	}
	cleaned := strings.Join(strings.Fields(raw), " ")
	if cleaned == "" || len([]rune(cleaned)) > maxPublicQualityErrorRunes {
		return ""
	}
	if chanshui.LooksLikeKey(cleaned) {
		return ""
	}
	if strings.ContainsAny(cleaned, "{}<>\\") {
		return ""
	}
	if strings.HasPrefix(cleaned, "/") || strings.Contains(cleaned, " /") {
		return ""
	}
	lower := strings.ToLower(cleaned)
	for _, token := range publicQualitySensitiveSubstrings {
		if strings.Contains(lower, token) {
			return ""
		}
	}
	if publicQualityIPv4Pattern.MatchString(lower) ||
		publicQualityIPv6Pattern.MatchString(lower) ||
		publicQualityBracketIPv6Pattern.MatchString(lower) ||
		publicQualityHostPortPattern.MatchString(lower) ||
		publicQualityHTTPStatusPattern.MatchString(lower) ||
		publicQualityStatusPhraseRegex.MatchString(lower) ||
		publicQualityJWTPattern.MatchString(lower) ||
		publicQualityKeyPrefixPattern.MatchString(lower) ||
		publicQualityEOFPattern.MatchString(lower) {
		return ""
	}
	return cleaned
}

// The public channel page receives only these fields, never raw probes,
// request/response text, audit URLs, remote audit IDs or arbitrary reasons.
type ChanshuiPublicSection struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Score  *float64 `json:"score"`
}

type ChanshuiPublicSummary struct {
	Score          *float64                `json:"score"`
	CandidateModel string                  `json:"candidate_model"`
	Sections       []ChanshuiPublicSection `json:"sections"`
}

func publicAuditToken(value any) string {
	s, ok := value.(string)
	if !ok || len(s) > 128 || chanshui.LooksLikeKey(s) {
		return ""
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("-_.:/", c):
			continue
		default:
			return ""
		}
	}
	return s
}

func publicAuditScore(value any) *float64 {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
		return nil
	}
	return &n
}

// PopulateGroupQualityAudit derives the historical method from the RESULT,
// not the current plan, which may have switched providers since this run.
func PopulateGroupQualityAudit(event *GroupQualityEvent, raw string) {
	if event == nil {
		return
	}
	event.QualityProvider = QualityProviderPelican
	if event.Status != "audit_pass" && event.Status != "audit_fail" {
		event.ErrorMessage = SanitizePublicQualityErrorMessage(event.Status, event.ErrorMessage)
		return
	}
	event.QualityProvider = QualityProviderChanshui
	event.ErrorMessage = ""
	if event.Status == "audit_pass" {
		event.Status = "success"
	} else {
		event.Status = "degraded"
	}
	var data map[string]any
	if len(raw) > chanshui.MaxBody || json.Unmarshal([]byte(raw), &data) != nil || data["provider"] != QualityProviderChanshui {
		return
	}
	verdict := chanshui.Object(data["verdict"])
	summary := &ChanshuiPublicSummary{
		Score:          publicAuditScore(chanshui.Object(verdict["total"])["score"]),
		CandidateModel: publicAuditToken(chanshui.Object(verdict["fingerprint"])["top_model"]),
		Sections:       []ChanshuiPublicSection{},
	}
	for _, name := range []string{"fingerprint", "injection", "hidden", "cache", "tools", "web", "knowledge", "thinking", "iq"} {
		section := chanshui.Object(verdict[name])
		if len(section) == 0 {
			continue
		}
		summary.Sections = append(summary.Sections, ChanshuiPublicSection{Name: name, Status: publicAuditToken(section["status"]), Score: publicAuditScore(section["score"])})
	}
	event.AuditSummary = summary
}
