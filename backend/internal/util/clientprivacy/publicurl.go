package clientprivacy

import "strings"

// publicVendorHosts 是官方公开的文档 / 控制台 / 支持 / 状态页主机，精确匹配
// （小写后整串相等，不做通配、不做后缀匹配）。
//
// 故意不收任何 API 主机（api.*、generativelanguage 等）：错误里出现 API 地址是
// 上游诊断，需要脱敏；出现帮助页、申诉页则是客户端和管理员真正需要读到的信息
// （曾把 https://console.anthropic.com/appeal 打码，客户端无法申诉）。
var publicVendorHosts = map[string]struct{}{
	"console.anthropic.com":    {},
	"docs.anthropic.com":       {},
	"support.anthropic.com":    {},
	"status.anthropic.com":     {},
	"www.anthropic.com":        {},
	"anthropic.com":            {},
	"platform.openai.com":      {},
	"help.openai.com":          {},
	"status.openai.com":        {},
	"community.openai.com":     {},
	"www.openai.com":           {},
	"openai.com":               {},
	"ai.google.dev":            {},
	"cloud.google.com":         {},
	"console.cloud.google.com": {},
	"support.google.com":       {},
}

// urlPlaceholderBase 起的 maxKeptURLs 个私有区字符，在后续各轮脱敏期间代替被
// 保留的公开链接。后面的正则只会写入 [redacted]，产生不了它们；输入里已经出现
// 这些字符时整体退回“所有链接都脱敏”，占位符因此无法被伪造。
const urlPlaceholderBase rune = '\uE000'

const maxKeptURLs = 32

// urlPattern 会把句末标点吞进链接；判定前先剥掉，免得它们被当成路径的一部分。
const trailingURLPunctuation = ".,;:!?)]}`\"'"

// urlKeeper 接管 urlPattern 那一轮的替换：官方公开链接换成占位符，其余照旧
// [redacted]。零值（enabled=false）就是旧行为。
//
// 占位符顶替的是整个匹配（含被吞进去的句末标点），与旧行为里的 [redacted] 同形，
// 所以后面各轮规则（赋值、凭据上下文、账号括号……）对它的处理与旧版逐字相同：
// 被它们吞掉的占位符随之消失，没被吞掉的在最后原样换回。
type urlKeeper struct {
	enabled bool
	kept    []string
}

func hasURLPlaceholder(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r >= urlPlaceholderBase && r < urlPlaceholderBase+maxKeptURLs
	})
}

func (k *urlKeeper) replace(match string) string {
	if !k.enabled || len(k.kept) >= maxKeptURLs ||
		!isPublicVendorURL(strings.TrimRight(match, trailingURLPunctuation)) {
		return redacted
	}
	k.kept = append(k.kept, match)
	return string(urlPlaceholderBase + rune(len(k.kept)-1))
}

// restore 一趟扫描把仍留在文本里的占位符换回原匹配，只认本次生成的占位符，
// 换回的内容不会被再次扫描。
func (k *urlKeeper) restore(s string) string {
	if len(k.kept) == 0 {
		return s
	}
	pairs := make([]string, 0, 2*len(k.kept))
	for i, match := range k.kept {
		pairs = append(pairs, string(urlPlaceholderBase+rune(i)), match)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// isPublicVendorURL 只放行 https://<白名单主机>[/路径]：不带用户信息、端口、
// 查询串、片段，路径只含 [A-Za-z0-9._~/%+-]，且路径里没有 API 密钥形态的串
// （后面的令牌规则看不到被保留的链接，这里先挡一道）。主机段（到第一个 /）与
// 白名单整串比较，用户信息 / 端口 / ? / # 混在主机段里自然匹配不上。
func isPublicVendorURL(link string) bool {
	rest, ok := strings.CutPrefix(link, "https://")
	if !ok {
		return false
	}
	host, path, _ := strings.Cut(rest, "/")
	if _, ok := publicVendorHosts[lowerASCII(host)]; !ok {
		return false
	}
	for i := 0; i < len(path); i++ {
		if !isPublicURLPathByte(path[i]) {
			return false
		}
	}
	return !tokenPattern.MatchString(path)
}

func isPublicURLPathByte(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return true
	}
	return strings.IndexByte("._~/%+-", b) >= 0
}

// lowerASCII 只折叠 A-Z。strings.ToLower 会把 U+0130（İ）折成 i，
// anthropİc.com 会冒充 anthropic.com。
func lowerASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}
