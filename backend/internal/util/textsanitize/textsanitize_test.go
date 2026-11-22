package textsanitize

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 工单 subject/content 是首例存储型 XSS，前端目前靠转义和 CSP 兜底。
// 入库侧必须先变成纯文本，下面这些 payload 全部要求清洗后不含标签/伪协议。
func TestPlainTextStripsHTMLPayloads(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "img onerror",
			input: `<img src=x onerror=alert(1)>你好`,
			want:  `你好`,
		},
		{
			name:  "svg onload",
			input: `<svg onload=alert(document.cookie)></svg>报障`,
			want:  `报障`,
		},
		{
			name:  "script tag keeps inner text only",
			input: `<script>alert(1)</script>正文`,
			want:  `alert(1)正文`,
		},
		{
			name:  "nested and broken tags",
			input: `<scr<script>ipt>alert(1)</script>`,
			want:  `ipt>alert(1)`,
		},
		{
			name:  "attribute value containing gt",
			input: `<a title="a>b" href="/x">点我</a>`,
			want:  `点我`,
		},
		{
			name:  "unterminated tag is dropped to end",
			input: `前缀<img src=x onerror=alert(1)`,
			want:  `前缀`,
		},
		{
			name:  "html comment",
			input: `前<!-- 注释 -->后`,
			want:  `前后`,
		},
		{
			name:  "encoded tag is decoded then stripped",
			input: `&lt;img src=x onerror=alert(1)&gt;ok`,
			want:  `ok`,
		},
		{
			name:  "double encoded tag",
			input: `&amp;lt;svg onload=alert(1)&amp;gt;ok`,
			want:  `ok`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlainText(tc.input)
			if got != tc.want {
				t.Fatalf("PlainText(%q) = %q, want %q", tc.input, got, tc.want)
			}
			assertNoMarkup(t, got)
		})
	}
}

func TestPlainTextStripsDangerousSchemes(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`javascript:alert(1)`, `alert(1)`},
		{`JavaScript:alert(1)`, `alert(1)`},
		{`java script:alert(1)`, `alert(1)`},
		{`java` + "\u200b" + `script:alert(1)`, `alert(1)`},
		{`data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==`, `;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==`},
		{`data:image/svg+xml;base64,PHN2Zz4=`, `;base64,PHN2Zz4=`},
		{`myjavascript:alert(1)`, `myjavascript:alert(1)`},
		{`普通链接说明：https://example.com`, `普通链接说明：https://example.com`},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := PlainText(tc.input)
			if got != tc.want {
				t.Fatalf("PlainText(%q) = %q, want %q", tc.input, got, tc.want)
			}
			assertNoDangerousScheme(t, got)
		})
	}
}

func TestPlainTextDecodesEntities(t *testing.T) {
	cases := map[string]string{
		`&#x27;`:         `'`,
		`&#39;`:          `'`,
		`&quot;`:         `"`,
		`A &amp; B`:      `A & B`,
		`&#x3c;b&#x3e;x`: `x`,
		`a &lt; b 保留符号`:  `a < b 保留符号`,
	}
	for input, want := range cases {
		if got := PlainText(input); got != want {
			t.Fatalf("PlainText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPlainTextKeepsPlainTextIntact(t *testing.T) {
	cases := []string{
		`模型返回 400，余额扣了 1.64 没退，订单号 A-20261123-001`,
		"第一行\n第二行\t缩进",
		`a < b 且 c > d`,
		`价格 5<10 元`,
		`邮箱 user+tag@example.com`,
	}
	for _, input := range cases {
		if got := PlainText(input); got != input {
			t.Fatalf("PlainText(%q) = %q, want unchanged", input, got)
		}
	}
}

func TestPlainTextRemovesZeroWidthAndBidiCharacters(t *testing.T) {
	invisible := []string{
		"\u200b", // zero width space
		"\u200c", // zero width non-joiner
		"\u200d", // zero width joiner
		"\u200e", // left-to-right mark
		"\u202e", // right-to-left override
		"\u2066", // left-to-right isolate
		"\ufeff", // BOM
		"\u00ad", // soft hyphen (Cf)
	}
	for _, inv := range invisible {
		raw := "管" + inv + "理" + inv + "员"
		if got := PlainText(raw); got != "管理员" {
			t.Fatalf("PlainText(%q) = %q, want %q", raw, got, "管理员")
		}
	}
}

func TestPlainTextRemovesControlCharacters(t *testing.T) {
	raw := "a\x00b\x01c\x07d\x1be\x7ff\x85g"
	if got := PlainText(raw); got != "abcdefg" {
		t.Fatalf("PlainText = %q, want %q", got, "abcdefg")
	}
}

func TestPlainTextTruncatesAfterSanitizing(t *testing.T) {
	long := strings.Repeat("界", 300)
	if got := PlainTextMax(long, 160); got != strings.Repeat("界", 160) {
		t.Fatalf("truncate rune count mismatch: %d", len([]rune(got)))
	}
	// 截断发生在清洗之后：前半截是超长 payload，截断点不能留下半截标签。
	payload := strings.Repeat("<b>", 100) + strings.Repeat("x", 300)
	if got := PlainTextMax(payload, 160); strings.ContainsAny(got, "<>") {
		t.Fatalf("truncated value still contains markup: %q", got)
	}
	if got := PlainTextMax("abc", 0); got != "abc" {
		t.Fatalf("maxRunes<=0 must not truncate, got %q", got)
	}
	if got := PlainTextMax("abc", 10); got != "abc" {
		t.Fatalf("shorter value must not change, got %q", got)
	}
}

func TestPlainTextEmptyAndWhitespace(t *testing.T) {
	if got := PlainText("   "); got != "" {
		t.Fatalf("whitespace-only = %q, want empty", got)
	}
	if got := PlainText("<img src=x onerror=alert(1)>"); got != "" {
		t.Fatalf("payload-only = %q, want empty", got)
	}
	if got := PlainText(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
	// 非法 UTF-8 不能原样落库（服务层还有 utf8.ValidString 校验兜底）。
	if got := PlainText("ok\xffbad"); got != "okbad" {
		t.Fatalf("invalid UTF-8 must be dropped, got %q", got)
	}
}

func TestPlainTextIsIdempotent(t *testing.T) {
	inputs := []string{
		`<img src=x onerror=alert(1)>你好`,
		`javascript:alert(1)`,
		`&lt;svg onload=alert(1)&gt;`,
		"第一行\n第二行",
	}
	for _, input := range inputs {
		once := PlainText(input)
		if twice := PlainText(once); twice != once {
			t.Fatalf("not idempotent for %q: %q -> %q", input, once, twice)
		}
	}
}

func assertNoMarkup(t *testing.T, value string) {
	t.Helper()
	for _, needle := range []string{"<img", "<svg", "<script", "onerror=", "onload="} {
		if strings.Contains(strings.ToLower(value), needle) {
			t.Fatalf("sanitized value still contains %q: %q", needle, value)
		}
	}
}

func assertNoDangerousScheme(t *testing.T, value string) {
	t.Helper()
	lower := strings.ToLower(value)
	for _, scheme := range []string{"javascript:", "vbscript:", "data:text/html", "data:image/svg+xml"} {
		for at := 0; ; {
			idx := strings.Index(lower[at:], scheme)
			if idx < 0 {
				break
			}
			pos := at + idx
			// 只有真正的 token 起点才算危险；"myjavascript:" 是普通文本。
			prev, _ := utf8.DecodeLastRuneInString(lower[:pos])
			if pos == 0 || !isSchemeWordRune(prev) {
				t.Fatalf("sanitized value still contains %q: %q", scheme, value)
			}
			at = pos + len(scheme)
		}
	}
}
