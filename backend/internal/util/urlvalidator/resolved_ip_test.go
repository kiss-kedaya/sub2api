package urlvalidator

import (
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// 回归：ValidateResolvedIP 原先只用标准库的 IsLoopback/IsPrivate/IsLinkLocal*/IsUnspecified，
// 放行了 CGNAT(100.64.0.0/10)、benchmarking(198.18.0.0/15)、保留段(240.0.0.0/4)、
// 全局组播(224.0.0.0/4) 以及若干文档/过渡段。同仓 custom_usage_http.go 另有一份
// 「完整版」清单，两份口径不一致。
//
// 现在合并为 IsPublicResolvedIP 单一来源，本测试锁定合并后的行为。
func TestIsPublicResolvedIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ip   string
		want bool
		why  string
	}{
		// 应当放行：公网单播
		{"8.8.8.8", true, "public IPv4"},
		{"1.1.1.1", true, "public IPv4"},
		{"104.18.32.7", true, "Cloudflare public IPv4"},
		{"2606:4700:4700::1111", true, "public IPv6"},

		// 回环 / 私网 / 链路本地
		{"127.0.0.1", false, "IPv4 loopback"},
		{"::1", false, "IPv6 loopback"},
		{"10.0.0.1", false, "RFC1918"},
		{"172.16.0.1", false, "RFC1918"},
		{"192.168.1.1", false, "RFC1918"},
		{"fd00::1", false, "IPv6 ULA"},
		{"169.254.169.254", false, "link-local cloud metadata"},
		{"fe80::1", false, "IPv6 link-local"},

		// 本次修复补上的段
		{"100.64.0.1", false, "CGNAT 100.64.0.0/10"},
		{"100.100.100.200", false, "阿里云元数据服务（CGNAT 段内）"},
		{"198.18.0.1", false, "benchmarking 198.18.0.0/15"},
		{"240.0.0.1", false, "reserved 240.0.0.0/4"},

		// 组播（标准库只覆盖链路本地组播）
		{"224.0.0.1", false, "IPv4 multicast all-hosts"},
		{"239.255.255.250", false, "IPv4 SSDP multicast"},
		{"ff02::1", false, "IPv6 multicast"},

		// 文档 / 过渡段
		{"192.0.2.1", false, "TEST-NET-1"},
		{"198.51.100.1", false, "TEST-NET-2"},
		{"203.0.113.1", false, "TEST-NET-3"},
		{"2001:db8::1", false, "IPv6 documentation"},
		{"2002::1", false, "6to4"},

		// 未指定
		{"0.0.0.0", false, "unspecified IPv4"},
		{"::", false, "unspecified IPv6"},

		// 非 2000::/3 的 IPv6 全局单播（如 NAT64 64:ff9b::/96）
		{"64:ff9b::1", false, "NAT64 outside 2000::/3"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			addr := netip.MustParseAddr(tc.ip)
			require.Equal(t, tc.want, IsPublicResolvedIP(addr), tc.why)
		})
	}
}

func TestIsPublicResolvedIP_InvalidAndZoned(t *testing.T) {
	t.Parallel()

	require.False(t, IsPublicResolvedIP(netip.Addr{}), "zero Addr must not be public")

	zoned := netip.MustParseAddr("fe80::1%eth0")
	require.False(t, IsPublicResolvedIP(zoned), "an address with a zone must never be treated as public")
}

// net.IP 的 IPv4 常常是 16 字节的 v4-in-v6 形式，IsPublicNetIP 必须先 Unmap，
// 否则会把公网 IPv4 误判成 IPv6 私网/非 2000::/3 而拒绝。
func TestIsPublicNetIP_HandlesIPv4InIPv6(t *testing.T) {
	t.Parallel()

	require.Len(t, net.ParseIP("8.8.8.8"), net.IPv6len, "precondition: ParseIP yields the 16-byte form")
	require.True(t, IsPublicNetIP(net.ParseIP("8.8.8.8")))
	require.True(t, IsPublicNetIP(net.ParseIP("104.18.32.7")))

	require.False(t, IsPublicNetIP(net.ParseIP("127.0.0.1")))
	require.False(t, IsPublicNetIP(net.ParseIP("100.100.100.200")))
	require.False(t, IsPublicNetIP(nil))
	require.False(t, IsPublicNetIP(net.IP{1, 2, 3}))
}

// customUsagePublicIP 现在委托给这份共享实现，行为必须保持一致。
func TestBlockedResolvedPrefixesAreStable(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, blockedResolvedPrefixes)
	for _, p := range blockedResolvedPrefixes {
		require.True(t, p.IsValid(), "every blocked prefix must parse")
	}
}
