package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 粗粒度指纹（loose 模式）。
//
// 旧的 loose 指纹把「IPv4 /24 或 IPv6 /64 + 完整 User-Agent」哈希成一个值，
// 所以浏览器自动升级一个小版本、WiFi 与蜂窝切换（IPv4 <-> IPv6）都会被判成「换了设备」。
// 粗粒度指纹把这几个维度拆开比较：
//   - User-Agent 只取「浏览器家族 + 操作系统家族」，不含版本号；
//   - IP 只在两边 IP 家族相同（都是 IPv4 或都是 IPv6）时才比较网段，家族不同则忽略 IP。
//
// 它以字符串形式和旧的 BindingHash 并存（见 RefreshTokenData.BindingCoarse / JWTClaims.BindingCoarse）：
// 旧哈希的算法不变，新旧版本混跑（金丝雀/回滚）时旧实例仍能正确比对，不会因为格式变化误撤销会话。

const coarseFieldSep = "|"

type coarseFingerprint struct {
	browser string // 浏览器/客户端家族，如 chrome、safari、wechat、curl
	os      string // 操作系统家族，如 windows、android、ios
	family  string // IP 家族：4、6，0 表示无法解析
	ipHash  string // IP 网段（v4 /24、v6 /64）的 8 位十六进制短哈希；family=0 时是原始字符串的短哈希
}

func (c coarseFingerprint) String() string {
	return strings.Join([]string{c.browser, c.os, c.family, c.ipHash}, coarseFieldSep)
}

func parseCoarseFingerprint(raw string) (coarseFingerprint, bool) {
	parts := strings.Split(raw, coarseFieldSep)
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return coarseFingerprint{}, false
	}
	return coarseFingerprint{browser: parts[0], os: parts[1], family: parts[2], ipHash: parts[3]}, true
}

// matches 报告两个粗粒度指纹是否属于「同一个客户端」：家族必须相同；
// IP 只在两边家族相同时才比较（IPv4<->IPv6 切换不算不一致）。
func (c coarseFingerprint) matches(other coarseFingerprint) bool {
	if c.browser != other.browser || c.os != other.os {
		return false
	}
	return c.family != other.family || c.ipHash == other.ipHash
}

// coarse 计算当前请求的粗粒度指纹；IP 与 UA 都为空时返回 false（无法绑定，调用方应放行）。
func (b *SessionBinding) coarse() (coarseFingerprint, bool) {
	if b == nil {
		return coarseFingerprint{}, false
	}
	rawIP := strings.TrimSpace(b.IP)
	ua := strings.TrimSpace(b.UserAgent)
	if rawIP == "" && ua == "" {
		return coarseFingerprint{}, false
	}
	browser, osFamily := coarseUserAgent(ua)
	family, ipHash := coarseIP(rawIP)
	return coarseFingerprint{browser: browser, os: osFamily, family: family, ipHash: ipHash}, true
}

// CoarseFingerprint 返回写入令牌的粗粒度指纹字符串；无法计算时返回空串。
func (b *SessionBinding) CoarseFingerprint() string {
	c, ok := b.coarse()
	if !ok {
		return ""
	}
	return c.String()
}

// Matches 报告当前请求指纹是否仍与令牌里记录的指纹一致。
//   - mode=off 或当前请求算不出指纹：放行（与原语义一致）；
//   - 先用旧算法的哈希比较（快路径，且新旧版本行为一致）；
//   - 哈希不同且是 loose 模式、令牌带粗粒度指纹时，再按粗粒度规则比较
//     （忽略浏览器/系统版本号、IPv4<->IPv6 切换不算不一致）；
//   - 旧令牌没有粗粒度指纹，哈希不同即不一致（旧行为）。
func (b *SessionBinding) Matches(mode, storedHash, storedCoarse string) bool {
	current := b.HashForMode(mode)
	if current == "" || storedHash == "" || current == storedHash {
		return true
	}
	if mode != config.SessionBindingModeLoose || storedCoarse == "" {
		return false
	}
	stored, ok := parseCoarseFingerprint(storedCoarse)
	if !ok {
		return false
	}
	cur, ok := b.coarse()
	if !ok {
		return true
	}
	return stored.matches(cur)
}

// BindingLabel 把粗粒度指纹转成日志用的低基数标签（如 chrome/windows/ipv4），
// 不含 IP 与完整 UA；旧令牌没有粗粒度指纹时返回 unknown。
func BindingLabel(coarse string) string {
	c, ok := parseCoarseFingerprint(coarse)
	if !ok {
		return "unknown"
	}
	family := "ip?"
	if c.family == "4" || c.family == "6" {
		family = "ipv" + c.family
	}
	return c.browser + "/" + c.os + "/" + family
}

// coarseIP 返回 IP 家族（4/6/0）与网段短哈希。无法解析的非空字符串按原样哈希（保守，仍参与比较）。
func coarseIP(raw string) (family, hash string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "0", ""
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return "0", coarseShortHash("raw:" + raw)
	}
	family = "6"
	if ip.To4() != nil {
		family = "4"
	}
	return family, coarseShortHash("ip" + family + ":" + sessionBindingNetworkPrefix(raw))
}

func coarseShortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// 浏览器家族标记。顺序重要：多数浏览器的 UA 里同时带 Chrome/Safari 字样，必须先匹配更具体的标记。
var coarseBrowserMarkers = []struct{ marker, name string }{
	{"MicroMessenger/", "wechat"},
	{"MQQBrowser/", "qqbrowser"},
	{"QQBrowser/", "qqbrowser"},
	{"EdgiOS/", "edge"},
	{"EdgA/", "edge"},
	{"Edg/", "edge"},
	{"Edge/", "edge"},
	{"OPR/", "opera"},
	{"OPiOS/", "opera"},
	{"Opera", "opera"},
	{"SamsungBrowser/", "samsung"},
	{"YaBrowser/", "yandex"},
	{"Vivaldi/", "vivaldi"},
	{"UCBrowser/", "uc"},
	{"Quark/", "quark"},
	{"DingTalk", "dingtalk"},
	{"Firefox/", "firefox"},
	{"FxiOS/", "firefox"},
	{"CriOS/", "chrome"},
	{"Chromium/", "chromium"},
	{"Chrome/", "chrome"},
	{"Safari/", "safari"},
}

// coarseUserAgent 返回 UA 的「浏览器家族 + 操作系统家族」，去掉全部版本号。
// 不是浏览器的客户端（curl、node、python-requests、okhttp 等）取产品名作为家族。
func coarseUserAgent(ua string) (browser, osFamily string) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return "none", "none"
	}
	browser = ""
	for _, m := range coarseBrowserMarkers {
		if strings.Contains(ua, m.marker) {
			browser = m.name
			break
		}
	}
	if browser == "" {
		browser = coarseProductToken(ua)
	}
	return browser, coarseOS(ua)
}

// coarseOS 的判断顺序同样重要：iOS 的 UA 里带 "like Mac OS X"，Android 的 UA 里带 "Linux"，
// ChromeOS 的 UA 里带 "X11; Linux"。
func coarseOS(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return "windows"
	case strings.Contains(ua, "Android"):
		return "android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"), strings.Contains(ua, "iOS"):
		return "ios"
	case strings.Contains(ua, "CrOS"):
		return "chromeos"
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Darwin"):
		return "macos"
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "X11"):
		return "linux"
	default:
		return "other"
	}
}

// coarseProductToken 取 UA 第一个产品名（"/" 或空格之前），只保留 [a-z0-9._-]，最长 24 个字符。
func coarseProductToken(ua string) string {
	end := len(ua)
	if i := strings.IndexAny(ua, "/ ("); i >= 0 {
		end = i
	}
	out := make([]byte, 0, 24)
	for _, r := range strings.ToLower(ua[:end]) {
		if len(out) >= 24 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			out = append(out, byte(r))
		}
	}
	if len(out) == 0 {
		return "other"
	}
	return string(out)
}
