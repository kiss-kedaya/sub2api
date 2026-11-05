package rpc

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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
	// node's message against a list we wrote. The node's own text is never kept.
	Reason     string
	contextErr error
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
	{"block range", "query range or result limit exceeded"},
	{"query returned more than", "query range or result limit exceeded"},
	{"response size exceeded", "query range or result limit exceeded"},
}

func classifyRemote(message string) string {
	lowered := strings.ToLower(message)
	for _, candidate := range remoteReasons {
		if strings.Contains(lowered, candidate.needle) {
			return candidate.reason
		}
	}
	return ""
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
	"sweep refused:",
	"confirmed gas top-up is insufficient",
	"RPC gas quote exceeds",
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
