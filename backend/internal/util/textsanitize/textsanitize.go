// Package textsanitize normalizes untrusted free-form text before the backend
// stores it. It is intentionally lossy: every field that goes through it is
// plain text, never rich text or HTML.
//
// 背景：工单 subject/content、api_keys.name、users.username 等字段会进入管理面
// 表格、邮件、通知与前端展示。此前这些字段入库不过滤，只靠前端转义 + CSP 兜底；
// 一旦任何页面改成 Markdown 或 v-html，存量 payload 立刻变成存储型 XSS。
// 统一在入库侧清洗成纯文本，前端渲染方式以后怎么改都不会把标记当代码执行。
package textsanitize

import (
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxEntityDecodePasses bounds nested HTML entity decoding so that values such
// as "&amp;lt;script&amp;gt;" cannot survive as markup after a single pass.
const maxEntityDecodePasses = 3

// dangerousSchemes are removed from plain text. Even after tag stripping a bare
// "javascript:..." left inside a field becomes clickable as soon as any page
// renders that field as a link (Markdown autolink, linkify, v-html href).
var dangerousSchemes = []string{
	"javascript:",
	"vbscript:",
	"data:text/html",
	"data:text/xml",
	"data:application/xml",
	"data:application/xhtml+xml",
	"data:image/svg+xml",
}

// PlainText converts untrusted text into a single-line/multi-line plain-text
// value with all HTML markup, dangerous URL schemes, control characters and
// invisible (zero-width / bidi override) characters removed. No length limit
// is applied; callers keep their existing validation limits.
func PlainText(raw string) string {
	return PlainTextMax(raw, 0)
}

// PlainTextMax behaves like PlainText and additionally truncates the result to
// maxRunes characters (runes, not bytes). maxRunes <= 0 means no truncation.
//
// 截断发生在清洗之后，所以超长 payload 不会因为「先截断再清洗」留下半截标签。
func PlainTextMax(raw string, maxRunes int) string {
	value := strings.ToValidUTF8(raw, "")
	value = normalizeNewlines(value)
	value = stripControlAndInvisible(value)

	// 先解码 HTML 实体再剥标签，否则 "&lt;img onerror=...&gt;" 会以实体形式
	// 落库，前端一旦按 HTML 渲染同样会变成标签。
	for i := 0; i < maxEntityDecodePasses && strings.ContainsRune(value, '&'); i++ {
		next := html.UnescapeString(value)
		if next == value {
			break
		}
		value = next
	}

	value = stripTags(value)
	// 实体解码可能产生新的控制字符/不可见字符，这里再清一遍。
	value = stripControlAndInvisible(value)
	value = stripDangerousSchemes(value)
	return truncateRunes(strings.TrimSpace(value), maxRunes)
}

func normalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// stripControlAndInvisible removes C0/C1 control characters (keeping "\n" and
// "\t") plus every Unicode format character: zero-width space/joiner and the
// bidi override characters used to spoof what the stored text looks like.
func stripControlAndInvisible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// C0/C1/DEL: drop.
		case unicode.Is(unicode.Cf, r):
			// U+200B..U+200F, U+202A..U+202E, U+2060..U+2064, U+FEFF, ...
			// 零宽与双向覆写字符：drop.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripTags removes HTML/XML tags while keeping their text content:
// "<b>hi</b>" -> "hi", "<img src=x onerror=alert(1)>ok" -> "ok".
//
// "<" that does not start a tag (e.g. "a < b", "5<10") is kept as text, which
// matches how a browser would render it. A tag opener without a terminator is
// dropped to the end of the value: the browser discards such a tag at EOF, and
// keeping the rest could re-form markup after concatenation.
func stripTags(s string) string {
	if !strings.ContainsRune(s, '<') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '<' {
			b.WriteByte(c)
			i++
			continue
		}
		end, isTag := scanTagEnd(s, i)
		if !isTag {
			b.WriteByte(c)
			i++
			continue
		}
		if end < 0 {
			break
		}
		i = end + 1
	}
	return b.String()
}

// scanTagEnd reports where the tag starting at s[start] ends. isTag=false means
// "<" is ordinary text; end=-1 means a tag opener that never terminates.
func scanTagEnd(s string, start int) (end int, isTag bool) {
	if start+1 >= len(s) {
		return 0, false
	}
	next := s[start+1]
	switch {
	case next == '/', next == '!', next == '?':
	case isASCIILetter(next):
	default:
		return 0, false
	}
	var quote byte
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			// 引号内的 ">" 属于属性值，不能当作标签结束。
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '>':
			return i, true
		}
	}
	return -1, true
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// stripDangerousSchemes removes javascript:/vbscript:/data:text/html style
// prefixes. Matching tolerates whitespace or invisible characters injected
// between the scheme letters, which browsers ignore when parsing URLs.
func stripDangerousSchemes(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); {
		if i == 0 || !isSchemeWordRune(runes[i-1]) {
			if consumed := matchDangerousScheme(runes[i:]); consumed > 0 {
				i += consumed
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func matchDangerousScheme(runes []rune) int {
	for _, target := range dangerousSchemes {
		if consumed, ok := matchSchemePrefix(runes, target); ok {
			return consumed
		}
	}
	return 0
}

func matchSchemePrefix(runes []rune, target string) (int, bool) {
	i := 0
	for ti := 0; ti < len(target); ti++ {
		for i < len(runes) && isSkippableInScheme(runes[i]) {
			i++
		}
		if i >= len(runes) {
			return 0, false
		}
		if unicode.ToLower(runes[i]) != rune(target[ti]) {
			return 0, false
		}
		i++
	}
	return i, true
}

func isSkippableInScheme(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// isSchemeWordRune reports whether r makes the preceding text part of a word,
// in which case what follows cannot start a URL scheme (avoids mangling
// "myjavascript:..." while still stripping a real "javascript:" token).
func isSchemeWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '-' || r == '.'
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}
