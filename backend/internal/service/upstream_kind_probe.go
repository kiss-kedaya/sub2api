package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Upstream kinds a probe can recognise from the API key alone.
//
// The kind is derived from which read-only endpoint answered with its own
// schema, never from the host name: relays are deployed under arbitrary
// domains, and the same domain can change software.
const (
	// UpstreamKindSub2API answers /v1/sub2api/billing or /v1/usage.
	UpstreamKindSub2API = "sub2api"
	// UpstreamKindNewAPI answers /api/usage/token/ with object=token_usage.
	UpstreamKindNewAPI = "newapi"
	// UpstreamKindOpenAICompatible answers the legacy
	// /v1/dashboard/billing/subscription endpoint, carries no new-api
	// fingerprint, and answers none of the above, so a balance can be read
	// without knowing which software serves it.
	UpstreamKindOpenAICompatible = "openai_compatible"
	// UpstreamKindOfficial is a provider's own API domain. Nothing is requested.
	UpstreamKindOfficial = "official"
	// UpstreamKindCustom answered none of the known endpoints.
	UpstreamKindCustom = "custom"
)

const (
	upstreamNewAPIUsagePath          = "/api/usage/token/"
	upstreamNewAPIStatusPath         = "/api/status"
	upstreamLegacySubscriptionPath   = "/v1/dashboard/billing/subscription"
	upstreamLegacyUsagePath          = "/v1/dashboard/billing/usage"
	upstreamKindProbeMaxBodyBytes    = 64 * 1024
	upstreamNewAPIMaxTokenNameRunes  = 64
	upstreamLegacyUnlimitedHardLimit = 100000000.0

	// upstreamNewAPIErrorType is what new-api puts in error.type. Its error
	// envelope is a fingerprint even when the endpoint asked no longer exists.
	upstreamNewAPIErrorType = "new_api_error"

	// upstreamNewAPIQuotaPerUnit is new-api's default QuotaPerUnit: 500000 quota
	// points are one US dollar. A site can change that ratio or switch its
	// display currency, and neither is visible through the key-scoped endpoint,
	// so every amount derived with it is marked as assumed.
	upstreamNewAPIQuotaPerUnit = 500000.0
)

// Balance sources recorded next to a balance so readers know how it was read.
const (
	UpstreamBalanceSourceSub2APIUsage       = "sub2api_usage"
	UpstreamBalanceSourceNewAPITokenUsage   = "newapi_token_usage"
	UpstreamBalanceSourceLegacySubscription = "openai_billing_subscription"
	// UpstreamBalanceSourceCustomUsage is the operator-configured extractor. It
	// only fills a balance the probe's own endpoints could not read, and the
	// figure keeps whatever unit the extractor declared.
	UpstreamBalanceSourceCustomUsage = "custom_usage"
)

type upstreamNewAPIUsageResponse struct {
	Code *bool `json:"code"`
	Data *struct {
		Object         *string  `json:"object"`
		Name           *string  `json:"name"`
		TotalGranted   *float64 `json:"total_granted"`
		TotalUsed      *float64 `json:"total_used"`
		TotalAvailable *float64 `json:"total_available"`
		UnlimitedQuota *bool    `json:"unlimited_quota"`
		ExpiresAt      *float64 `json:"expires_at"`
		// user_quota is the account-level balance. new-api only returns it when
		// the token has allow_user_quota_query turned on; when it is off the field
		// is absent rather than zero.
		UserQuota *struct {
			TotalAvailable *float64 `json:"total_available"`
			TotalUsed      *float64 `json:"total_used"`
		} `json:"user_quota"`
	} `json:"data"`
}

// parseUpstreamNewAPIUsageResponse reads a new-api GET /api/usage/token/
// response into the same balance fields the Sub2API parser produces, so the
// account list renders one shape regardless of the upstream software.
//
// Amounts are converted to US dollars with new-api's default QuotaPerUnit and
// flagged unit_assumed. An unlimited token reports total_available = 0, which
// must not be read as an empty balance: unlimited_quota decides.
func parseUpstreamNewAPIUsageResponse(body []byte) (map[string]any, error) {
	var response upstreamNewAPIUsageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	// object=token_usage is what tells new-api apart from any other JSON API
	// that happens to answer this path with 200.
	if response.Data == nil || response.Data.Object == nil || *response.Data.Object != "token_usage" {
		return nil, fmt.Errorf("unexpected new-api usage response schema")
	}
	usage := response.Data
	data := map[string]any{
		"is_valid":     true,
		"unit":         "USD",
		"unit_assumed": true,
	}
	if usage.Name != nil {
		if name := sanitizeUpstreamBalanceText(*usage.Name, upstreamNewAPIMaxTokenNameRunes); name != "" {
			data["plan_name"] = name
		}
	}
	unlimited := usage.UnlimitedQuota != nil && *usage.UnlimitedQuota
	if unlimited {
		data["unlimited"] = true
	}
	if used, ok := upstreamNewAPIQuotaToUSD(usage.TotalUsed); ok {
		data["quota_used"] = used
	}
	if !unlimited {
		if remaining, ok := upstreamNewAPIQuotaToUSD(usage.TotalAvailable); ok {
			data["remaining"] = remaining
		}
		if granted, ok := upstreamNewAPIQuotaToUSD(usage.TotalGranted); ok {
			data["quota_limit"] = granted
		}
	}
	if usage.UserQuota != nil {
		if balance, ok := upstreamNewAPIQuotaToUSD(usage.UserQuota.TotalAvailable); ok {
			data["wallet_balance"] = balance
		}
		if used, ok := upstreamNewAPIQuotaToUSD(usage.UserQuota.TotalUsed); ok {
			data["wallet_used"] = used
		}
	}
	// expires_at is a Unix timestamp in seconds; 0 means the token never expires.
	if expiresAt, ok := finiteUpstreamBalanceNumber(usage.ExpiresAt); ok && expiresAt > 0 {
		if formatted, ok := upstreamUnixSecondsToRFC3339(expiresAt); ok {
			data["expires_at"] = formatted
		}
	}
	if _, hasRemaining := data["remaining"]; !hasRemaining && !unlimited {
		if _, hasWallet := data["wallet_balance"]; !hasWallet {
			return nil, fmt.Errorf("new-api usage response carries no balance")
		}
	}
	return data, nil
}

func upstreamNewAPIQuotaToUSD(value *float64) (float64, bool) {
	quota, ok := finiteUpstreamBalanceNumber(value)
	if !ok {
		return 0, false
	}
	return quota / upstreamNewAPIQuotaPerUnit, true
}

type upstreamLegacySubscriptionResponse struct {
	Object             *string  `json:"object"`
	HardLimitUSD       *float64 `json:"hard_limit_usd"`
	SoftLimitUSD       *float64 `json:"soft_limit_usd"`
	SystemHardLimitUSD *float64 `json:"system_hard_limit_usd"`
	AccessUntil        *float64 `json:"access_until"`
}

type upstreamLegacyUsageResponse struct {
	TotalUsage *float64 `json:"total_usage"`
}

// parseUpstreamLegacySubscriptionResponse reads the legacy OpenAI-compatible
// /v1/dashboard/billing/subscription response. hard_limit_usd is the granted
// total (remaining plus used), so the remaining balance needs total_usage from
// /v1/dashboard/billing/usage, which reports cents.
//
// usageBody may be nil when the usage endpoint did not answer; the limit is
// still stored, without a remaining amount, rather than presenting the granted
// total as if it were what is left.
func parseUpstreamLegacySubscriptionResponse(subscriptionBody, usageBody []byte) (map[string]any, error) {
	var subscription upstreamLegacySubscriptionResponse
	if err := json.Unmarshal(subscriptionBody, &subscription); err != nil {
		return nil, err
	}
	// The 1e8 sentinel is new-api's "unlimited quota" marker, not a granted
	// total. A caller that got this far without identifying new-api must not
	// present the marker as a limit, and must not present "unlimited" unless the
	// new-api usage payload says unlimited_quota itself.
	if upstreamLegacyBillingIsNewAPISentinel(subscriptionBody) {
		return nil, fmt.Errorf("new-api billing sentinel is not an OpenAI balance")
	}
	hardLimit, ok := finiteUpstreamBalanceNumber(subscription.HardLimitUSD)
	if !ok {
		return nil, fmt.Errorf("unexpected billing subscription response schema")
	}
	data := map[string]any{"is_valid": true, "unit": "USD", "quota_limit": hardLimit}
	if len(usageBody) > 0 {
		var usage upstreamLegacyUsageResponse
		if err := json.Unmarshal(usageBody, &usage); err == nil {
			if cents, ok := finiteUpstreamBalanceNumber(usage.TotalUsage); ok {
				used := cents / 100
				data["quota_used"] = used
				data["remaining"] = math.Max(hardLimit-used, 0)
			}
		}
	}
	if accessUntil, ok := finiteUpstreamBalanceNumber(subscription.AccessUntil); ok && accessUntil > 0 {
		if formatted, ok := upstreamUnixSecondsToRFC3339(accessUntil); ok {
			data["expires_at"] = formatted
		}
	}
	return data, nil
}

// upstreamLegacyBillingIsNewAPISentinel reports whether a legacy billing
// subscription payload carries new-api's "unlimited quota" sentinel.
//
// new-api serves /v1/dashboard/billing/subscription for OpenAI-client
// compatibility and fills hard_limit_usd, soft_limit_usd and
// system_hard_limit_usd with 1e8 for a token whose quota is unlimited. Real
// OpenAI billing never reports that number, so the sentinel identifies new-api
// even on a site whose /api/status is 404 and whose responses carry no
// new-api header. The amount is a marker, never a granted total: it must not be
// shown as a limit and must not be used to compute a balance.
func upstreamLegacyBillingIsNewAPISentinel(body []byte) bool {
	var subscription upstreamLegacySubscriptionResponse
	if err := json.Unmarshal(body, &subscription); err != nil {
		return false
	}
	for _, value := range []*float64{
		subscription.HardLimitUSD,
		subscription.SoftLimitUSD,
		subscription.SystemHardLimitUSD,
	} {
		if limit, ok := finiteUpstreamBalanceNumber(value); ok && limit >= upstreamLegacyUnlimitedHardLimit {
			return true
		}
	}
	return false
}

// upstreamLegacyBillingUsedUSD reads the spend from a legacy
// /v1/dashboard/billing/usage answer (cents). The figure is real spend even on
// a new-api site that serves the endpoint for compatibility, so it is kept as
// "used" while the balance itself is reported as not provided.
func upstreamLegacyBillingUsedUSD(body []byte) (float64, bool) {
	if len(body) == 0 {
		return 0, false
	}
	var usage upstreamLegacyUsageResponse
	if err := json.Unmarshal(body, &usage); err != nil {
		return 0, false
	}
	cents, ok := finiteUpstreamBalanceNumber(usage.TotalUsage)
	if !ok || cents < 0 {
		return 0, false
	}
	return cents / 100, true
}

// upstreamSiteRootURL resolves a path against the origin of an account's base
// URL, ignoring whatever path the base carries.
//
// Accounts store the model endpoint (https://host/v1, https://host/openai/v1),
// while new-api serves its usage endpoint at the site root, with a trailing
// slash that the router requires (a missing slash is answered with a 301, and
// probes do not follow redirects). buildOpenAIEndpointURL would produce
// /v1/api/usage/token and drop the slash, so it cannot be used for this.
func upstreamSiteRootURL(baseURL, path string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + path
	}
	root := url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: path}
	return root.String()
}

// upstreamUnixSecondsToRFC3339 formats an upstream Unix timestamp. Values that
// are not whole seconds in a sane range are rejected rather than rendered as a
// date in year 50000.
func upstreamUnixSecondsToRFC3339(seconds float64) (string, bool) {
	const maxUnixSeconds = 4102444800 // 2100-01-01T00:00:00Z
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > maxUnixSeconds {
		return "", false
	}
	return time.Unix(int64(seconds), 0).UTC().Format(time.RFC3339), true
}

// upstreamProbeAnswered reports whether a response is a successful one whose
// body is worth parsing.
func upstreamProbeAnswered(result upstreamBillingProbeHTTPResult) bool {
	return result.failure == "" && result.statusCode >= 200 && result.statusCode < 300 && len(result.body) > 0
}

// upstreamProbeRejectedKey reports whether the upstream answered that the key
// is not accepted or asked the caller to back off. Either stops the sequence:
// a relay may count rejected keys against the caller's IP, and continuing to
// other paths after 429 ignores the back-off the upstream just requested.
func upstreamProbeRejectedKey(result upstreamBillingProbeHTTPResult) bool {
	switch result.statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// normalizeUpstreamKind keeps only the kinds this build knows about, so a
// value written by a newer build or edited by hand cannot reach the UI.
func normalizeUpstreamKind(value string) string {
	switch strings.TrimSpace(value) {
	case UpstreamKindSub2API, UpstreamKindNewAPI, UpstreamKindOpenAICompatible, UpstreamKindOfficial, UpstreamKindCustom:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

// upstreamNewAPIHeaderNames are the response headers the new-api family sets on
// every answer, including error answers for paths it does not serve.
//
// They exist because new-api deliberately answers the legacy OpenAI billing
// endpoints for compatibility: a billing payload on its own can no longer say
// which software served it. These headers are that distinction.
var upstreamNewAPIHeaderNames = []string{"X-New-Api-Version", "X-Oneapi-Request-Id"}

// upstreamNewAPIHeaderEvidence reports the new-api fingerprint carried by a
// response header and names the signal that matched.
//
// Headers are read regardless of status code: a 404 from a new-api site still
// carries its version header, which is exactly how a site that also answers
// /v1/dashboard/billing is told apart from a real OpenAI-billing relay.
func upstreamNewAPIHeaderEvidence(header http.Header) (string, bool) {
	for _, name := range upstreamNewAPIHeaderNames {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return "header:" + strings.ToLower(name), true
		}
	}
	return "", false
}

// upstreamNewAPIErrorEvidence reports whether a body is new-api's error
// envelope, {"error":{"message":…,"type":"new_api_error"}}.
//
// The type name only counts inside a JSON error object, so an HTML page or a
// log line quoting the word is not a fingerprint.
func upstreamNewAPIErrorEvidence(body []byte) (string, bool) {
	if len(body) == 0 || !bytes.Contains(bytes.ToLower(body), []byte(upstreamNewAPIErrorType)) {
		return "", false
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Error) == 0 {
		return "", false
	}
	if !bytes.Contains(bytes.ToLower(envelope.Error), []byte(upstreamNewAPIErrorType)) {
		return "", false
	}
	return "body:" + upstreamNewAPIErrorType, true
}

// upstreamNewAPIStatusEvidence reports whether /api/status answered with the
// document new-api and its one-api ancestor serve.
//
// The document carries no literal "new-api" marker, so the fields only that
// family returns are the fingerprint: new-api adds quota_per_unit /
// display_in_currency / enable_data_export, while one-api's status always pairs
// a version with start_time.
func upstreamNewAPIStatusEvidence(body []byte) (string, bool) {
	var status struct {
		Data *struct {
			Version           *string  `json:"version"`
			StartTime         *float64 `json:"start_time"`
			QuotaPerUnit      *float64 `json:"quota_per_unit"`
			DisplayInCurrency *bool    `json:"display_in_currency"`
			EnableDataExport  *bool    `json:"enable_data_export"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &status); err != nil || status.Data == nil {
		return "", false
	}
	if status.Data.QuotaPerUnit != nil || status.Data.DisplayInCurrency != nil || status.Data.EnableDataExport != nil {
		return "status:newapi_fields", true
	}
	if status.Data.Version != nil && strings.TrimSpace(*status.Data.Version) != "" && status.Data.StartTime != nil {
		return "status:oneapi_fields", true
	}
	return "", false
}
