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
	contextErr error
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

var safeRPCStatus = regexp.MustCompile(`^RPC (?:HTTP request failed \(status [1-5][0-9]{2}\)|node rejected request \(code -?[0-9]{1,10}\))$`)

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
	if safeRPCStatus.MatchString(message) {
		return message
	}
	switch message {
	case "independent RPC confirmation is unavailable or disagrees":
		return message
	case "RPC request is invalid", "RPC response is invalid", "RPC connection failed", "RPC request failed",
		"awaiting canonical receipt and required confirmations",
		"awaiting gas payer transaction confirmation",
		"confirmed transaction reverted; automatic replacement/funding refused",
		"legacy or ambiguous transaction; reconcile hashes manually before releasing address",
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
