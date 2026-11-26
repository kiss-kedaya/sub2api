package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// readOnlyMethods are the JSON-RPC methods that only read chain state. Repeating
// one of them can never change anything, so it is the only kind of call this
// package may send a second time on its own.
//
// It is an explicit allow-list, not a deny-list of "dangerous" names: a method
// nobody classified (a new one, a provider extension, eth_sendRawTransaction,
// eth_sendTransaction, anything personal_/admin_/debug_) is treated as one that
// may change state. Such a call
//   - is never retried here, and
//   - never rides a pooled connection (see callHint.fresh), so a connection the
//     server closed while it sat idle cannot turn a broadcast into "result
//     unknown" - the failure mode that already has to be settled through receipts.
var readOnlyMethods = map[string]struct{}{
	"eth_chainId":               {},
	"eth_blockNumber":           {},
	"eth_gasPrice":              {},
	"eth_getBalance":            {},
	"eth_getTransactionCount":   {},
	"eth_getBlockByNumber":      {},
	"eth_getLogs":               {},
	"eth_getTransactionByHash":  {},
	"eth_getTransactionReceipt": {},
	"eth_call":                  {},
}

func isReadOnlyMethod(method string) bool {
	_, ok := readOnlyMethods[method]
	return ok
}

// staleConnMarkers are error texts of a connection that died while idle and could
// not be told apart from a live one until a request was written to it. Only errors
// without an exported type are matched by text (HTTP/2 keeps them unexported).
var staleConnMarkers = []string{
	"server closed idle connection",
	"connection reset by peer",
	"broken pipe",
	"use of closed network connection",
	"goaway",
	"client connection lost",
	"client conn is closed",
	"client conn not usable",
}

// isStaleConnError reports a failure that looks like the peer had already closed
// the connection the request was written to. The caller decides whether retrying
// is allowed; this only recognises the shape of the error. A deadline or a
// cancellation is never one: the connection may well be alive and slow.
func isStaleConnError(err error) bool {
	if err == nil {
		return false
	}
	var wrapped *url.Error
	if errors.As(err, &wrapped) {
		err = wrapped.Err // url.Error text embeds the request URL; judge what is inside it
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.ECONNABORTED):
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range staleConnMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
