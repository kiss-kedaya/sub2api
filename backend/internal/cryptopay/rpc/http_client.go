package rpc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

type lookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

type publicRPCNetwork struct {
	lookup lookupIPAddrFunc
	dial   dialContextFunc
}

// NewPublicHTTPClient builds the direct-only HTTP client used for configured
// RPC endpoints. It rejects private DNS answers and dials the exact address
// that was checked, preventing rebinding between validation and connection.
func NewPublicHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return newPublicHTTPClient(timeout, net.DefaultResolver.LookupIPAddr, dialer.DialContext)
}

func newPublicHTTPClient(timeout time.Duration, lookup lookupIPAddrFunc, dial dialContextFunc) *http.Client {
	network := publicRPCNetwork{lookup: lookup, dial: dial}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            network.dialContext,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  timeout,
			MaxResponseHeaderBytes: 16 << 10,
			DisableKeepAlives:      true,
			DisableCompression:     true,
			ForceAttemptHTTP2:      true,
		},
	}
}

func (n publicRPCNetwork) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("invalid rpc destination")
	}

	var ips []net.IPAddr
	if literal, parseErr := netip.ParseAddr(strings.TrimSuffix(host, ".")); parseErr == nil {
		ips = []net.IPAddr{{IP: net.IP(literal.AsSlice()), Zone: literal.Zone()}}
	} else {
		ips, err = n.lookup(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("rpc DNS lookup failed")
		}
	}

	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || ip.Zone != "" || !urlvalidator.IsPublicResolvedIP(addr) {
			return nil, errors.New("rpc destination is not public")
		}
	}

	for _, ip := range ips {
		conn, dialErr := n.dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.New("rpc connection failed")
}
