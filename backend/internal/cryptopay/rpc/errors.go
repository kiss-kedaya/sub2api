package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Failure is safe for logs and payment records. Its diagnostic fields contain
// only fixed categories and numeric protocol status, never an endpoint, body,
// transport error string, or RPC error message supplied by a node.
type Failure struct {
	Kind       string
	HTTPStatus int
	RPCCode    int
	// Reason is one of the fixed phrases in remoteReasons, picked by matching the
	// node's message against a list we wrote.
	Reason string
	// Message is the node-supplied free text. It is never part of Error(), which
	// is the durable-record boundary; Detail() sanitises it for operator logs and
	// diagnostics only.
	Message string
	// Host is the endpoint hostname (no scheme, path, query or credentials) that
	// produced the failure, so a scan failure can name a node without writing a
	// configured URL.
	Host string
	// SpanLimit is the largest eth_getLogs block span the node said it accepts,
	// parsed from its rejection text. Zero means the node named no limit. The
	// pool remembers it per endpoint so an oversized query goes to a node that
	// can serve it instead of being re-sent to the one that just refused it.
	SpanLimit uint64
	// Quota is set when an HTTP 402/429/503 answer says the provider's daily or
	// monthly allowance (or prepaid balance) is used up. Waiting seconds cannot
	// help: the pool keeps that endpoint out of rotation for much longer than an
	// ordinary rate limit instead of spending a request on it for every call.
	Quota      bool
	contextErr error
}

// Failure categories. They answer "what should the caller do", not "what did
// the node say". Transport/timeout is retryable and must never be written down
// as a business failure; rejected-known means the node definitively refused the
// exact request; auth-config needs an operator; unknown needs a human to look.
const (
	CategoryTransport     = "transport"
	CategoryTimeout       = "timeout"
	CategoryRejectedKnown = "rejected-known"
	CategoryAuthConfig    = "auth-config"
	// CategoryStale is a node that answered from behind the block the caller
	// asked about. It is a health signal, not a statement about the payment.
	CategoryStale   = "stale"
	CategoryUnknown = "unknown"
)

// rejectedKnownReasons are deterministic node rejections of a request. Retrying
// the same bytes cannot change them, but they say nothing about a payment job.
var rejectedKnownReasons = map[string]bool{
	ReasonAlreadyKnown:                    true,
	"nonce too low":                       true,
	"nonce too high":                      true,
	"replacement transaction underpriced": true,
	"transaction underpriced":             true,
	"insufficient funds for gas":          true,
	"intrinsic gas too low":               true,
	"fee cap below base fee":              true,
	"transaction pool is full":            true,
	"execution reverted":                  true,
}

// authNeedles turn endpoint/auth misconfiguration into an actionable category.
var authNeedles = []string{
	"invalid api key", "invalid api-key", "invalid apikey", "api key not valid",
	"unauthorized", "forbidden", "authentication", "invalid project id",
	"project id not found", "invalid token", "missing api key",
}

func remoteCategory(reason, message string) string {
	lowered := strings.ToLower(message)
	for _, needle := range authNeedles {
		if strings.Contains(lowered, needle) {
			return CategoryAuthConfig
		}
	}
	if rejectedKnownReasons[reason] {
		return CategoryRejectedKnown
	}
	return CategoryUnknown
}

// Category classifies a failure for retry and log decisions.
func (e *Failure) Category() string {
	if e == nil {
		return CategoryUnknown
	}
	switch e.Kind {
	case "transport":
		if errors.Is(e.contextErr, context.DeadlineExceeded) {
			return CategoryTimeout
		}
		return CategoryTransport
	case "http":
		switch {
		case e.HTTPStatus == 401 || e.HTTPStatus == 403:
			return CategoryAuthConfig
		case e.HTTPStatus == 408 || e.HTTPStatus == 429 || e.HTTPStatus >= 500:
			return CategoryTransport
		}
		return CategoryUnknown
	case "remote":
		return remoteCategory(e.Reason, e.Message)
	case "stale":
		return CategoryStale
	}
	return CategoryUnknown
}

// IsTransient reports whether the same request may succeed on another endpoint
// or a later attempt. A canceled caller context is not transient.
func (e *Failure) IsTransient() bool {
	if e == nil || errors.Is(e.contextErr, context.Canceled) {
		return false
	}
	switch e.Category() {
	case CategoryTransport, CategoryTimeout:
		return true
	}
	return false
}

// CategoryOf classifies any error; anything that is not an rpc.Failure is
// unknown to this package.
func CategoryOf(err error) string {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Category()
	}
	return CategoryUnknown
}

// IsTransient reports whether err is a retryable transport or timeout failure.
func IsTransient(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.IsTransient()
}

// IsRejectedKnown reports a deterministic node rejection (nonce too low,
// already known, underpriced, insufficient funds, ...).
func IsRejectedKnown(err error) bool {
	return CategoryOf(err) == CategoryRejectedKnown
}

var (
	urlPattern = regexp.MustCompile(`[a-z][a-z0-9+.-]*://\S+`)
	keyPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|token|secret|password|passwd|authorization)\b\s*[=:]\s*\S+`)
	// 0x-prefixed data of address/hash length and long bare hex runs can be a
	// private key or a signed transaction; neither belongs in a log.
	hexPattern = regexp.MustCompile(`0x[0-9a-fA-F]{20,}|[0-9a-fA-F]{40,}`)
)

// SanitizeNodeMessage keeps an operator-useful shape of a node's text while
// removing URLs, credential pairs and long hex values. Nodes echo request data
// (addresses, signed bytes), so this is the only form allowed to leave the
// process in a diagnostic field.
func SanitizeNodeMessage(message string) string {
	text := strings.Join(strings.Fields(message), " ")
	if text == "" {
		return ""
	}
	text = urlPattern.ReplaceAllString(text, "[url]")
	text = keyPattern.ReplaceAllString(text, "$1=[hidden]")
	text = hexPattern.ReplaceAllString(text, "[hex]")
	const limit = 160
	if len(text) > limit {
		text = text[:limit-4] + "…"
	}
	return text
}

// Detail is the sanitised node text for logs. It never enters Error().
func (e *Failure) Detail() string {
	if e == nil {
		return ""
	}
	return SanitizeNodeMessage(e.Message)
}

// DetailOf returns the sanitised node text of err, if any.
func DetailOf(err error) string {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Detail()
	}
	return ""
}

// HostOf returns the failing endpoint host, if it is known.
func HostOf(err error) string {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Host
	}
	return ""
}

// ReasonAlreadyKnown is what a node answers when the same signed transaction is
// already in its pool. Re-sending identical bytes is then a no-op, not a failure.
const ReasonAlreadyKnown = "transaction already known"

// remoteReasons turns the free text of a node's JSON-RPC error into one of our
// own fixed phrases. Order matters: the first needle found wins, so a longer
// message is listed before the shorter one it contains.
var remoteReasons = []struct{ needle, reason string }{
	{"replacement transaction underpriced", "replacement transaction underpriced"},
	{"transaction underpriced", "transaction underpriced"},
	{"already known", ReasonAlreadyKnown},
	{"known transaction", ReasonAlreadyKnown},
	{"invalid api key", "invalid API key"},
	{"invalid api-key", "invalid API key"},
	{"unauthorized", "unauthorized"},
	{"forbidden", "forbidden"},
	{"nonce too low", "nonce too low"},
	{"nonce too high", "nonce too high"},
	{"insufficient funds", "insufficient funds for gas"},
	{"intrinsic gas too low", "intrinsic gas too low"},
	{"max fee per gas less than block base fee", "fee cap below base fee"},
	{"fee cap less than block base fee", "fee cap below base fee"},
	{"txpool is full", "transaction pool is full"},
	{"transaction pool is full", "transaction pool is full"},
	{"execution reverted", "execution reverted"},
	{"rate limit", "rate limited"},
	{"too many requests", "rate limited"},
	{"block range", ReasonRangeLimit},
	{"query returned more than", ReasonRangeLimit},
	{"response size exceeded", ReasonRangeLimit},
}

// ReasonRangeLimit is the fixed phrase for a node refusing an eth_getLogs query
// because its block range or result size is too large. It is the one rejection
// the scanner answers by splitting the query, so it must stay a stable string.
const ReasonRangeLimit = "query range or result limit exceeded"

func classifyRemote(message string) string {
	reason, _ := classifyRemoteDetail(message)
	return reason
}

// maxNodeMessageBytes bounds the node text that is ever classified or kept. A
// node controls the whole response, and every classification pattern runs over
// it: an unbounded message turned one reply into seconds of CPU.
const maxNodeMessageBytes = 512

// truncateNodeMessage cuts a node's text to maxNodeMessageBytes on a rune boundary.
func truncateNodeMessage(message string) string {
	if len(message) <= maxNodeMessageBytes {
		return message
	}
	return strings.ToValidUTF8(message[:maxNodeMessageBytes], "")
}

// classifyRemoteDetail is classifyRemote plus the block span the node stated, if
// any, found in one pass over the (bounded) text.
func classifyRemoteDetail(message string) (string, uint64) {
	message = truncateNodeMessage(message)
	limit, hasLimit := rangeLimitFromMessage(message)
	lowered := strings.ToLower(message)
	for _, candidate := range remoteReasons {
		if strings.Contains(lowered, candidate.needle) {
			return candidate.reason, limit
		}
	}
	// Providers word the range limit in many ways ("ranges over 100 blocks",
	// "requested range is over limit of 100", "limited to 0 - 50 blocks range").
	// The needles above only know the common ones; a number next to a range or
	// block phrase is the other tell-tale.
	if hasLimit {
		return ReasonRangeLimit, limit
	}
	return "", 0
}

// rangeLimitPatterns pull the permitted block span out of a node's rejection.
// Every pattern needs a range/blocks phrase next to the number, so an unrelated
// number in an error ("429", "10 requests per second") is never mistaken for a
// span. A trailing k/K multiplies by one thousand ("up to a 2K block range").
var rangeLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ranges? (?:over|above|greater than|larger than|exceeding) (\d[\d,]*)(k)?\s*blocks?`),
	regexp.MustCompile(`(?i)range (?:is )?(?:over|above|greater than|larger than|exceeds?) (?:the )?(?:limit|max(?:imum)?)(?: of)?:? (\d[\d,]*)(k)?`),
	regexp.MustCompile(`(?i)limited to (?:\d+ ?- ?)?(\d[\d,]*)(k)?\s*blocks?`),
	regexp.MustCompile(`(?i)(?:up to|at most|no more than|maximum of|max of) (?:a )?(\d[\d,]*)(k)?[ -]?blocks?`),
	regexp.MustCompile(`(?i)block range[^0-9]{0,30}(\d[\d,]*)(k)?`),
}

// Anything outside this window is a parse accident, not a provider limit: a
// span of a few blocks would make the scanner crawl, and one past a million
// blocks is no limit at all.
const (
	minLearnedSpan = 10
	maxLearnedSpan = 1_000_000
)

// rangeLimitFromMessage returns the block span a node says it accepts.
func rangeLimitFromMessage(message string) (uint64, bool) {
	for _, pattern := range rangeLimitPatterns {
		match := pattern.FindStringSubmatch(message)
		if match == nil {
			continue
		}
		digits := strings.ReplaceAll(match[1], ",", "")
		value, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			continue
		}
		if len(match) > 2 && match[2] != "" {
			value *= 1000
		}
		if value >= minLearnedSpan && value <= maxLearnedSpan {
			return value, true
		}
	}
	return 0, false
}

// SpanLimitOf returns the block span a node named when it refused err's
// request, or zero.
func SpanLimitOf(err error) uint64 {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.SpanLimit
	}
	return 0
}

// quotaPattern recognises "the allowance is used up" wording. It is only applied
// to HTTP 402/429/503 answers, never to a JSON-RPC error of a transaction
// ("insufficient funds for gas" is a wallet problem, not a provider quota).
var quotaPattern = regexp.MustCompile(`(?i)\b(?:daily|monthly|quota|ceiling|insufficient (?:ru|balance|credits?)|out of (?:credits?|requests?)|credits? (?:exhausted|depleted)|payment required|plan limit|capacity limit)\b`)

// annotateFromErrorBody fills a failure from the body of a non-200 answer. Only
// the sanitised Detail() ever leaves the process; Error() stays a fixed phrase.
func annotateFromErrorBody(failure *Failure, body []byte) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return
	}
	code := 0
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) > 0 {
		var nested struct {
			Code    json.Number `json:"code"`
			Message string      `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &nested) == nil && nested.Message != "" {
			text = nested.Message
			if value, err := strconv.Atoi(nested.Code.String()); err == nil {
				code = value
			}
		} else {
			var plain string
			if json.Unmarshal(envelope.Error, &plain) == nil && plain != "" {
				text = plain
			}
		}
	}
	// The node's own words are used to classify and then dropped: an HTTP error
	// page can echo the request URL, API key included, and nothing in this process
	// can promise to redact every spelling of that before it reaches a log. What
	// survives is a fixed phrase from remoteReasons, a number and a flag.
	text = truncateNodeMessage(text)
	reason, limit := classifyRemoteDetail(text)
	failure.Reason = reason
	failure.SpanLimit = limit
	switch failure.HTTPStatus {
	case 402, 429, 503:
		failure.Quota = quotaPattern.MatchString(text)
	}
	if failure.Reason == ReasonRangeLimit && failure.Kind == "http" {
		// The provider refused the query's size and said so: that is a request
		// rejection the scanner answers by splitting, not an outage. Report it
		// like the JSON-RPC range errors other nodes return (code -32005 on BSC).
		failure.Kind = "remote"
		failure.RPCCode = code
		if failure.RPCCode == 0 {
			failure.RPCCode = -32602
		}
	}
}

// IsAlreadyKnown reports whether err is a node saying it already holds the
// transaction we tried to send.
func IsAlreadyKnown(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Kind == "remote" && failure.Reason == ReasonAlreadyKnown
}

// Unwrap retains only safe context sentinels. Transport/url errors can contain
// full endpoint credentials and must never enter the returned error chain.
func (e *Failure) Unwrap() error { return e.contextErr }

func NewTransportFailure(err error) *Failure {
	failure := &Failure{Kind: "transport"}
	if errors.Is(err, context.Canceled) {
		failure.contextErr = context.Canceled
	} else if errors.Is(err, context.DeadlineExceeded) {
		failure.contextErr = context.DeadlineExceeded
	}
	return failure
}

func (e *Failure) Error() string {
	switch e.Kind {
	case "http":
		return fmt.Sprintf("RPC HTTP request failed (status %d)", e.HTTPStatus)
	case "remote":
		if e.Reason != "" {
			return fmt.Sprintf("RPC node rejected request (code %d): %s", e.RPCCode, e.Reason)
		}
		return fmt.Sprintf("RPC node rejected request (code %d)", e.RPCCode)
	case "request":
		return "RPC request is invalid"
	case "response":
		return "RPC response is invalid"
	case "transport":
		return "RPC connection failed"
	case "stale":
		return "RPC endpoint is behind the requested block"
	default:
		return "RPC request failed"
	}
}

// SafeError is the only error-string boundary for sweep summaries and durable
// sweep errors. Unknown causes get a useful fixed message instead of persisting
// database driver parameters, endpoint credentials, or node-controlled text.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Error()
	}
	if errors.Is(err, ErrIndependentEvidence) {
		return ErrIndependentEvidence.Error()
	}
	if errors.Is(err, context.Canceled) {
		return "Payment operation canceled; reconcile transaction status before retrying"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Payment operation timed out; reconcile transaction status before retrying"
	}
	return SafeRecordedError(err.Error())
}

var safeRPCStatus = regexp.MustCompile(`^RPC (?:HTTP request failed \(status [1-5][0-9]{2}\)|node rejected request \(code -?[0-9]{1,10}\)(?:: (?:` + remoteReasonPattern() + `))?)$`)

func remoteReasonPattern() string {
	seen := map[string]bool{}
	var parts []string
	for _, candidate := range remoteReasons {
		if !seen[candidate.reason] {
			seen[candidate.reason] = true
			parts = append(parts, regexp.QuoteMeta(candidate.reason))
		}
	}
	return strings.Join(parts, "|")
}

// jobErrorGaveUp is what the sweeper appends when a job stops being retried.
// Only digits can vary, so the suffix itself carries nothing that needs hiding.
var gaveUpSuffix = regexp.MustCompile(` \(gave up after [0-9]{1,6} attempts\)$`)

// safeLiveBalance covers "live balance N is below planned amount M": two plain
// integers written by the sweeper, never node text.
var safeLiveBalance = regexp.MustCompile(`^live balance [0-9]{1,78} is below planned amount [0-9]{1,78}$`)

var safeScopeMessage = regexp.MustCompile(`^chain "[a-z0-9_-]{1,32}" is outside the requested sweep scope$`)

// safeBusinessPrefixes are messages this repository writes itself. They carry
// no endpoint, credential or node-supplied text, so a sweep record can keep
// them: "gas top-up is insufficient" and "gas payer is busy" are different
// operator actions and must not collapse into one opaque sentence.
var safeBusinessPrefixes = []string{
	// Written by the sweeper when it proves a signed transaction can never be
	// mined and deliberately frees the address. Keep in step with
	// sweeper.staleReleasePrefix; a test in the sweeper package pins it.
	"stale pending released:",
	// Written by the sweeper when a swept transaction is proven off the
	// canonical chain. Keep in step with sweeper.sweptRollbackPrefix.
	"swept rollback released:",
	"sweep refused:",
	"confirmed gas top-up is insufficient",
	"RPC gas quote exceeds",
	// Gas funding waits written by the sweeper. They are fixed sentences (no
	// endpoint, key or node text) and name an operator action, so a sweep record
	// must keep them instead of collapsing into the generic payment sentence.
	"insufficient native gas budget",
	"gas wallet native balance cannot cover",
	"sweep gas budget is not configured",
	"address has another unresolved transaction",
	"unfinished job ",
	"reserved transaction bytes are missing",
	"broadcast returned a different transaction hash",
	"wallet is not initialized",
	"wallet returned invalid gas payer",
	"sweep database is not configured",
	"confirmation depth is not configured",
	"sweep requires at least two database connections",
	"sweep already running in another instance",
	"cryptopay is disabled",
	"invalid sweep job id",
	"sweep job ",
	"no RPC pool for chain",
}

// boundedBusinessError only forwards a message that matches our own wording and
// cannot hide an endpoint URL, a hex address or an unbounded upstream body.
func boundedBusinessError(message string) (string, bool) {
	if message == "" || len(message) > 160 {
		return "", false
	}
	if strings.Contains(message, "://") || strings.Contains(message, "0x") {
		return "", false
	}
	for _, prefix := range safeBusinessPrefixes {
		if strings.HasPrefix(message, prefix) {
			return message, true
		}
	}
	return "", false
}

// SafeRecordedError also protects records written by previous versions. Only
// known fixed messages or bounded numeric RPC statuses survive; free-form old
// errors can contain credentials in any URL path or arbitrary upstream text.
func SafeRecordedError(message string) string {
	if message == "" {
		return ""
	}
	// A job that stops retrying gets " (gave up after N attempts)" appended. That
	// suffix is plain digits, so keep it and judge only what comes before it:
	// otherwise the real reason is always replaced by the generic sentence.
	if loc := gaveUpSuffix.FindStringIndex(message); loc != nil && loc[0] > 0 {
		return safeRecordedBase(message[:loc[0]]) + message[loc[0]:]
	}
	return safeRecordedBase(message)
}

func safeRecordedBase(message string) string {
	if message == "" {
		return ""
	}
	if safeRPCStatus.MatchString(message) || safeLiveBalance.MatchString(message) || safeScopeMessage.MatchString(message) {
		return message
	}
	switch message {
	case "independent RPC confirmation is unavailable or disagrees":
		return message
	case "current credited sweep balance or address assignment is invalid",
		"current credited balance is below the per-address sweep minimum",
		"unsigned sweep job changed before balance refresh",
		"no matching token/decimals configured for chain/contract",
		"invalid sweep plan",
		"no rpc pool for chain",
		"invalid balanceOf result",
		"signed transaction does not match reserved chain/nonce/gas",
		"signed transaction sender does not match reserved payer",
		"signed transaction fee does not match request",
		"signed gas payment differs from reserved payment",
		"invalid reserved token/amount",
		"signed token transfer differs from immutable plan",
		"incomplete or mismatched receipt",
		"receipt block is no longer canonical",
		"persisted transaction hash does not match signed bytes",
		"gas top-up superseded by another transaction at the same nonce; the address will be funded again":
		return message
	case "RPC request is invalid", "RPC response is invalid", "RPC connection failed", "RPC request failed",
		"awaiting canonical receipt and required confirmations",
		"awaiting gas payer transaction confirmation",
		"confirmed transaction reverted; automatic replacement/funding refused",
		"confirmed gas top-up reverted; automatic replacement/funding refused",
		"legacy or ambiguous transaction; reconcile hashes manually before releasing address",
		"sweep authorization expired or missing; no new broadcast was sent",
		"wallet security lock is active; no new broadcast was sent",
		"wallet signing is disabled; no new broadcast was sent",
		"wallet vault is unavailable; no new broadcast was sent",
		"wallet is not configured; no new broadcast was sent",
		"sweep state persistence failed; reconcile the recorded transaction before retrying",
		"sweep already running in another instance",
		"Payment operation canceled; reconcile transaction status before retrying",
		"Payment operation timed out; reconcile transaction status before retrying":
		return message
	default:
		if message, ok := boundedBusinessError(message); ok {
			return message
		}
		return "Payment operation failed; reconcile transaction status before retrying"
	}
}
