package rpc

import (
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
)

// deniedRPC hosts are refused on every path: configured lists, automatic
// chainlist discovery and any pool reload.
//
// 0.48.club answered eth_gasPrice with 0x1 (one wei) while healthy BSC nodes
// reported 0.05–1 gwei. The sweeper trusted it, priced a 999.99 USDT sweep at
// roughly 0.019 gwei, and the transaction sat unmined for 43 hours while the
// address stayed reserved. The whole 48.club domain is denied rather than the
// single host: the operator already proved it can serve junk, and 0.48.club and
// rpc-bsc.48.club are one codebase away from swapping behaviour.
var deniedRPCDomains = []string{
	"48.club",
}

// IsDeniedRPCEndpoint reports whether the endpoint belongs to a denied domain.
// Unparsable input is not denied here; the caller validates shape separately.
func IsDeniedRPCEndpoint(endpoint string) bool {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return false
	}
	host := ""
	if parsed, err := url.Parse(trimmed); err == nil {
		host = parsed.Hostname()
	}
	if host == "" {
		// Bare host:port / host forms that url.Parse reports as a path.
		host = trimmed
		if i := strings.IndexAny(host, "/:"); i >= 0 {
			host = host[:i]
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, domain := range deniedRPCDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// filterRPCList drops denied hosts and keeps the operator's ordering.
func filterRPCList(endpoints string) []string {
	var list []string
	for _, raw := range strings.Split(endpoints, ",") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || IsDeniedRPCEndpoint(trimmed) {
			continue
		}
		list = append(list, trimmed)
	}
	return list
}

// Gas-price sanity. A node that answers below the floor is not asked again for
// a price in this call; the floor itself is the answer so a signed transaction
// is never underpriced just because one endpoint is broken.
var defaultGasPriceFloorWei atomic.Int64

// SetDefaultGasPriceFloor installs the process-wide floor in wei.
func SetDefaultGasPriceFloor(wei int64) {
	if wei > 0 {
		defaultGasPriceFloorWei.Store(wei)
	}
}

// DefaultGasPriceFloorWei returns the installed floor (0 when unset).
func DefaultGasPriceFloorWei() int64 {
	return defaultGasPriceFloorWei.Load()
}

// Gwei converts a gwei count to wei. Kept here so chain floors read as gwei.
func Gwei(value float64) int64 {
	return int64(value * 1e9)
}

// FormatWeiGwei renders wei as a gwei string for logs.
func FormatWeiGwei(wei int64) string {
	return strconv.FormatFloat(float64(wei)/1e9, 'f', -1, 64)
}
