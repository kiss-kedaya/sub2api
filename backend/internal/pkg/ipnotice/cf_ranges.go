package ipnotice

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Cloudflare publishes its edge ranges at these two stable URLs. They are the
// authoritative source; the snapshot below is only a bootstrap fallback so that
// detection keeps working when the fetch fails (air-gapped host, DNS outage,
// egress proxy that blocks cloudflare.com).
const (
	CloudflareRangesV4URL = "https://www.cloudflare.com/ips-v4"
	CloudflareRangesV6URL = "https://www.cloudflare.com/ips-v6"

	// CloudflareRangesRefreshInterval is how often the live list replaces the
	// snapshot. Cloudflare changes its ranges rarely, so a daily refresh is
	// plenty and keeps the outbound request volume negligible.
	CloudflareRangesRefreshInterval = 24 * time.Hour

	cloudflareRangeFetchTimeout = 10 * time.Second
	// cloudflareRangesMaxBody bounds one response. The real files are well under
	// 2 KB; anything larger is a captive portal or an error page, not a range list.
	cloudflareRangesMaxBody = 64 << 10
)

// defaultCloudflareRanges is the embedded snapshot, taken from
// https://www.cloudflare.com/ips-v4 and /ips-v6. It must only ever be used as a
// fallback: when the live fetch succeeds the fetched list replaces it.
var defaultCloudflareRanges = []string{
	// IPv4
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	// IPv6
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

// nonPublicSourceRanges are the private, loopback, link-local and unspecified
// networks that must never be attributed to a real client. An internal proxy or
// a sidecar making gateway calls from these addresses would otherwise look like
// one IP serving several accounts.
var nonPublicSourceRanges = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"100.64.0.0/10", // RFC 6598 carrier-grade NAT
	"::1/128",
	"fc00::/7",
	"fe80::/10",
}

// sourceExclusionCloudflare and friends are the reasons ExcludedSourceIP
// reports. They are stable strings so logs and tests can assert on them.
const (
	SourceExclusionCloudflare = "cloudflare_edge"
	SourceExclusionPrivate    = "non_public"
	SourceExclusionInvalid    = "invalid"
)

type prefixSet struct {
	v4    []netip.Prefix
	v6    []netip.Prefix
	count int
}

func parsePrefixSet(raw []string) *prefixSet {
	set := &prefixSet{}
	for _, value := range raw {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		if prefix.Addr().Is4() {
			set.v4 = append(set.v4, prefix)
		} else {
			set.v6 = append(set.v6, prefix)
		}
	}
	set.count = len(set.v4) + len(set.v6)
	return set
}

// contains reports whether addr falls inside the set. Addresses are compared in
// unmapped form so an IPv4-mapped IPv6 source still matches the IPv4 ranges.
func (s *prefixSet) contains(addr netip.Addr) bool {
	if s == nil || !addr.IsValid() {
		return false
	}
	candidates := s.v4
	if addr.Is6() {
		candidates = s.v6
	}
	for _, prefix := range candidates {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

var (
	cloudflareRanges    atomic.Pointer[prefixSet]
	nonPublicRanges     = parsePrefixSet(nonPublicSourceRanges)
	cloudflareFetchOnce sync.Once
	// cloudflareHTTPClient is a package variable so tests can substitute a stub
	// transport instead of reaching the network.
	cloudflareHTTPClient = &http.Client{Timeout: cloudflareRangeFetchTimeout}
)

func init() {
	cloudflareRanges.Store(parsePrefixSet(defaultCloudflareRanges))
}

// parseSourceAddr canonicalizes a client address for range comparison. It
// returns false for anything that is not a plain address.
func parseSourceAddr(raw string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return netip.Addr{}, false
	}
	if addr.Zone() != "" {
		// A zone-scoped address (fe80::1%eth0) is only meaningful on its own
		// link, so it can never identify a public client.
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// IsCloudflareIP reports whether raw is inside a Cloudflare edge range. The
// direction matters: a request that arrives FROM a Cloudflare range reached the
// gateway through Cloudflare's shared egress, so the address says nothing about
// the real client and must never be attributed to a single account.
func IsCloudflareIP(raw string) bool {
	addr, ok := parseSourceAddr(raw)
	if !ok {
		return false
	}
	return cloudflareRanges.Load().contains(addr)
}

// IsNonPublicIP reports whether raw is a private, loopback, link-local or
// unspecified address.
func IsNonPublicIP(raw string) bool {
	addr, ok := parseSourceAddr(raw)
	if !ok {
		return false
	}
	if nonPublicRanges.contains(addr) {
		return true
	}
	// netip also knows about the IPv6 unspecified address and multicast, which
	// are not in the explicit list above.
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast()
}

// ExcludedSourceIP decides whether a source address may contribute to sharing
// detection at all. The second return value is the reason, which is logged once
// per skipped request at debug level so an operator can see why an IP was
// ignored without turning the hot path into a log spammer.
//
// Cloudflare comes first: a Cloudflare edge address is public, so a
// private-address check would let it through and blame Cloudflare instead of the
// real client.
func ExcludedSourceIP(raw string) (string, bool) {
	if _, ok := parseSourceAddr(raw); !ok {
		return SourceExclusionInvalid, true
	}
	if IsCloudflareIP(raw) {
		return SourceExclusionCloudflare, true
	}
	if IsNonPublicIP(raw) {
		return SourceExclusionPrivate, true
	}
	return "", false
}

// CloudflareRangeCount reports how many ranges are currently active. It exists
// for startup logging and tests, never for the request path.
func CloudflareRangeCount() int {
	set := cloudflareRanges.Load()
	if set == nil {
		return 0
	}
	return set.count
}

// RefreshCloudflareRanges replaces the active ranges with the live Cloudflare
// list. Failure is non-fatal by design: the previously active list stays in
// place, so a transient network error never widens or narrows the exclusion set
// in a way that could start auto-listing Cloudflare egress.
//
// A response that parses to zero ranges is treated as a failure too, so an
// HTML error page can never silently empty the exclusion set.
func RefreshCloudflareRanges(ctx context.Context, client *http.Client) error {
	if client == nil {
		client = cloudflareHTTPClient
	}
	v4, errV4 := fetchCloudflareRanges(ctx, client, CloudflareRangesV4URL)
	v6, errV6 := fetchCloudflareRanges(ctx, client, CloudflareRangesV6URL)
	if errV4 != nil && errV6 != nil {
		return errors.Join(errV4, errV6)
	}
	fetched := parsePrefixSet(append(append([]string{}, v4...), v6...))
	if fetched.count == 0 {
		return errors.New("cloudflare refresh returned no usable ranges")
	}
	// A partial fetch (one family failing) still counts: the successful family is
	// authoritative for its own addresses, and the failed one keeps working
	// through the snapshot only if we merge rather than replace.
	if errV4 != nil || errV6 != nil {
		previous := cloudflareRanges.Load()
		if previous != nil {
			if errV4 != nil {
				fetched.v4 = previous.v4
			}
			if errV6 != nil {
				fetched.v6 = previous.v6
			}
			fetched.count = len(fetched.v4) + len(fetched.v6)
		}
	}
	cloudflareRanges.Store(fetched)
	slog.Info("cloudflare ranges refreshed", "v4", len(fetched.v4), "v6", len(fetched.v6))
	return nil
}

func fetchCloudflareRanges(ctx context.Context, client *http.Client, url string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, cloudflareRangeFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("cloudflare range fetch returned " + resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, cloudflareRangesMaxBody))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, 16)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := netip.ParsePrefix(line); err != nil {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, errors.New("cloudflare range response contained no prefixes")
	}
	return out, nil
}

// StartCloudflareRangesRefresher launches one background refresher for the whole
// process: an immediate attempt at startup and then one per refresh interval.
// Calling it more than once is a no-op, so every router construction can call it
// without spawning duplicate loops.
func StartCloudflareRangesRefresher() {
	cloudflareFetchOnce.Do(func() {
		go func() {
			refresh := func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*cloudflareRangeFetchTimeout)
				defer cancel()
				if err := RefreshCloudflareRanges(ctx, cloudflareHTTPClient); err != nil {
					// Debug, not warn: the embedded snapshot is a correct fallback
					// and this runs once a day, so a blocked egress would
					// otherwise print a warning forever.
					slog.Debug("cloudflare ranges refresh failed, keeping the current list", "error", err, "ranges", CloudflareRangeCount())
				}
			}
			refresh()
			ticker := time.NewTicker(CloudflareRangesRefreshInterval)
			defer ticker.Stop()
			for range ticker.C {
				refresh()
			}
		}()
	})
}
