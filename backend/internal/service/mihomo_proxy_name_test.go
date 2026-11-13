package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTruncateUTF8Bytes(t *testing.T) {
	require.Equal(t, "", truncateUTF8Bytes("", 10))
	require.Equal(t, "abc", truncateUTF8Bytes("abc", 3))
	require.Equal(t, "abc", truncateUTF8Bytes("abc", 10))
	require.Equal(t, "ab", truncateUTF8Bytes("abc", 2))

	// A multi-byte rune is dropped rather than split.
	require.Equal(t, "", truncateUTF8Bytes("中", 1))
	require.Equal(t, "", truncateUTF8Bytes("中", 2))
	require.Equal(t, "中", truncateUTF8Bytes("中", 3))
	require.Equal(t, "中", truncateUTF8Bytes("中文", 4))
	require.Equal(t, "🇨", truncateUTF8Bytes("🇨", 4))
	require.Equal(t, "", truncateUTF8Bytes("🇨", 3))

	for _, s := range []string{
		strings.Repeat("中文节点", 40),
		strings.Repeat("🇨🇳节点", 40),
		strings.Repeat("a", 300),
	} {
		for max := 0; max <= len(s)+1; max++ {
			got := truncateUTF8Bytes(s, max)
			require.LessOrEqual(t, len(got), max)
			require.True(t, utf8.ValidString(got), "truncating %q to %d bytes produced invalid UTF-8", s, max)
			require.True(t, strings.HasPrefix(s, got))
		}
	}
}

func TestMihomoProxyNameFitsNameColumnByteLimit(t *testing.T) {
	require.Equal(t, "Mihomo · short node", mihomoProxyName("short node", "ignored"))
	require.Equal(t, "Mihomo · fallback", mihomoProxyName("", "fallback"))
	require.Equal(t, "Mihomo · fallback", mihomoProxyName("   ", "fallback"))

	for name, displayName := range map[string]string{
		"ascii": strings.Repeat("a", 200),
		"cjk":   strings.Repeat("中文节点", 40),
		"emoji": strings.Repeat("🇨🇳节点", 40),
		"mixed": "🇨🇳 中文节点 " + strings.Repeat("长名称", 40),
	} {
		t.Run(name, func(t *testing.T) {
			got := mihomoProxyName(displayName, "fallback")
			require.LessOrEqual(t, len(got), proxyNameMaxBytes)
			require.Greater(t, len(got), len(mihomoProxyNamePrefix))
			require.True(t, utf8.ValidString(got))
			require.True(t, strings.HasPrefix(got, mihomoProxyNamePrefix))
			require.True(t, strings.HasPrefix(displayName, strings.TrimPrefix(got, mihomoProxyNamePrefix)))
		})
	}
}
