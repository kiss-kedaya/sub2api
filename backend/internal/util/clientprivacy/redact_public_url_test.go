package clientprivacy

import (
	"encoding/json"
	"maps"
	"math/rand"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 线上真实事件的形状：Anthropic 组织被停用，申诉页同时出现在 details.appeal_url
// 和 message 里。reference / request id 换成了同形状的合成值，不带真实标识。
const orgOnHoldDocument = `{"error":{"details":{"appeal_url":"https://console.anthropic.com/appeal","error_code":"organization_on_hold"},"message":"This organization has been disabled. An organization admin can appeal at https://console.anthropic.com/appeal (Reference: TS-00000000-0000-4000-8000-000000000000) (request id: req_011SyntheticRequestID000)","type":"invalid_request_error"},"request_id":"req_011SyntheticRequestID000","type":"error"}`

const (
	appealURL = "https://console.anthropic.com/appeal"
	docsURL   = "https://platform.openai.com/docs/guides/error-codes/api-errors"
)

// auditedPublicVendorHosts 与实现里的白名单各写一份：改白名单必须同时改这里，
// 逼着改动经过审查。
var auditedPublicVendorHosts = []string{
	"console.anthropic.com", "docs.anthropic.com", "support.anthropic.com", "status.anthropic.com",
	"www.anthropic.com", "anthropic.com",
	"platform.openai.com", "help.openai.com", "status.openai.com", "community.openai.com",
	"www.openai.com", "openai.com",
	"ai.google.dev", "cloud.google.com", "console.cloud.google.com", "support.google.com",
}

func TestPublicVendorHostListIsExact(t *testing.T) {
	got := slices.Sorted(maps.Keys(publicVendorHosts))
	want := slices.Sorted(slices.Values(auditedPublicVendorHosts))
	if !slices.Equal(got, want) {
		t.Fatalf("allow-list changed without review\n got: %v\nwant: %v", got, want)
	}
}

func TestTextKeepsPublicVendorURLs(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"bare url", appealURL},
		{"url at start", appealURL + " is the appeal page"},
		{"url at end", "An organization admin can appeal at " + appealURL},
		{"apex host without path", "https://anthropic.com"},
		{"trailing slash", "https://www.anthropic.com/"},
		{"uppercase host", "https://CONSOLE.Anthropic.COM/appeal"},
		{"path charset", "https://docs.anthropic.com/en/docs/a%20b/~c+d_e-f.g"},
		{"openai docs sentence", "For more information on this error, read the docs: " + docsURL + "."},
		{"google docs", "See https://ai.google.dev/gemini-api/docs/troubleshooting for details"},
		{"cloud console", "enable it at https://console.cloud.google.com/apis/library/cloudaicompanion.googleapis.com"},
		{"two urls", "see " + appealURL + " and https://support.anthropic.com/en/articles/123"},
		{"newline separated", appealURL + "\nhttps://help.openai.com/en/articles/1"},
		{"dotted path stays untouched by later passes", "https://docs.anthropic.com/v1.2.3.4/a.b.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Text(tc.input); got != tc.input {
				t.Fatalf("public vendor URL was redacted\n input: %q\n   got: %q", tc.input, got)
			}
		})
	}
	for _, host := range auditedPublicVendorHosts {
		for _, link := range []string{"https://" + host, "https://" + host + "/", "https://" + host + "/a/b-c_d.e~f%20g+h"} {
			if got := Text("see " + link + " now"); got != "see "+link+" now" {
				t.Errorf("allow-listed %q was redacted: %q", link, got)
			}
		}
	}
}

// urlPattern 会把句末标点吞进链接：链接保留、标点留在链接之外原样输出。
func TestTextKeepsPublicURLBeforeSentencePunctuation(t *testing.T) {
	for _, tail := range []string{".", ",", ";", ":", "!", "?", ")", "]", "}", "`", ").", "),", "...", "?!", ".)"} {
		input := "read the docs: " + docsURL + tail + " Thanks"
		if got := Text(input); got != input {
			t.Errorf("tail %q: got %q", tail, got)
		}
		if got := Text(docsURL + tail); got != docsURL+tail {
			t.Errorf("tail %q at end of string: got %q", tail, got)
		}
	}
	for _, wrap := range [][2]string{{"(", ")"}, {"[", "]"}, {"{", "}"}, {"`", "`"}, {`"`, `"`}, {"'", "'"}, {"<", ">"}} {
		input := "see " + wrap[0] + docsURL + wrap[1] + " now"
		if got := Text(input); got != input {
			t.Errorf("wrapper %q: got %q", wrap[0]+wrap[1], got)
		}
	}
	// 剥掉句末标点不能让不合规的链接变合规：非白名单主机、带查询串 / 片段 / 端口的照旧整条脱敏。
	for _, input := range []string{"https://relay.example.test/v1.", docsURL + "?x=1.", docsURL + "#frag.", docsURL + ":8443."} {
		if got := Text(input); got != redacted {
			t.Errorf("%q: got %q", input, got)
		}
	}
}

func TestURLKeeperKeepsWholeMatchAndBoundsCount(t *testing.T) {
	k := urlKeeper{enabled: true}
	first := k.replace(docsURL + ").")
	if first == redacted || strings.Contains(first, "openai") {
		t.Fatalf("public URL was not swapped for a placeholder: %q", first)
	}
	if got := k.replace("https://relay.example.test/x"); got != redacted {
		t.Fatalf("private URL must be redacted: %q", got)
	}
	if got := k.restore("[" + first + "]"); got != "["+docsURL+").]" {
		t.Fatalf("restore changed the swallowed punctuation: %q", got)
	}
	if got := k.restore(string(urlPlaceholderBase + 5)); got != string(urlPlaceholderBase+5) {
		t.Fatalf("restore touched a placeholder it never issued: %q", got)
	}
	for i := 1; i < maxKeptURLs; i++ {
		if got := k.replace(docsURL + strconv.Itoa(i)); got == redacted {
			t.Fatalf("URL %d of %d was redacted too early", i+1, maxKeptURLs)
		}
	}
	if got := k.replace(docsURL); got != redacted {
		t.Fatalf("URL beyond the cap must be redacted: %q", got)
	}
	off := urlKeeper{}
	if got := off.replace(docsURL); got != redacted {
		t.Fatalf("a disabled keeper must redact everything: %q", got)
	}
}

// 以下都必须和旧行为逐字一致：整条链接换成 [redacted]。
func TestTextStillRedactsNonPublicURLs(t *testing.T) {
	for _, link := range []string{
		// 上游 / 中转地址
		"https://relay.example.test/v1/messages",
		"https://api.anthropic.com/v1/messages",
		"https://api.openai.com/v1/responses",
		"https://generativelanguage.googleapis.com/v1beta/models",
		// 近似域名
		"https://console.anthropic.com.evil.test/appeal",
		"https://anthropic.com.evil.test/",
		"https://console-anthropic.com/appeal",
		"https://evil.test/console.anthropic.com",
		"https://sub.console.anthropic.com/appeal",
		"https://xconsole.anthropic.com/appeal",
		"https://console.anthropic.com.cn/appeal",
		// 协议
		"http://console.anthropic.com/appeal",
		"HTTPS://console.anthropic.com/appeal",
		"Https://console.anthropic.com/appeal",
		"ftp://console.anthropic.com/appeal",
		"wss://console.anthropic.com/appeal",
		"git+https://console.anthropic.com/appeal",
		"xhttps://console.anthropic.com/appeal",
		"http\u017f://console.anthropic.com/appeal",
		// IP 字面量
		"https://203.0.113.7/appeal",
		"https://[2001:db8::1]/appeal",
		"https://3232235777/appeal",
		// 用户信息 / 端口
		"https://user:synthetic-pass@console.anthropic.com/appeal",
		"https://console.anthropic.com@evil.test/appeal",
		"https://evil.test@console.anthropic.com/appeal",
		"https://console.anthropic.com:8443/appeal",
		"https://console.anthropic.com:443/",
		"https://console.anthropic.com:/appeal",
		// 查询串 / 片段
		"https://console.anthropic.com/appeal?token=abc",
		"https://console.anthropic.com/appeal?a=1#b",
		"https://console.anthropic.com/appeal#frag",
		"https://console.anthropic.com/appeal?x",
		"https://console.anthropic.com?x=1",
		"https://console.anthropic.com#x",
		"https://console.anthropic.com/?x",
		// 编码 / 同形字符
		"https://console%2eanthropic.com/appeal",
		"https://console.anthropic.com%2f.evil.test/",
		"https://console.anthrop\u0130c.com/appeal",
		"https://console\uff0eanthropic.com/appeal",
		"https://console.anthropic.com\u200b/appeal",
		"https://console.anthropic.com/\u00e4",
		// 路径里不在 [A-Za-z0-9._~/%+-] 内的字符
		"https://console.anthropic.com/a:b", "https://console.anthropic.com/a@b", "https://console.anthropic.com/a=b",
		"https://console.anthropic.com/a&b", "https://console.anthropic.com/a,b", "https://console.anthropic.com/a;b",
		"https://console.anthropic.com/a(b)", "https://console.anthropic.com/a*b", "https://console.anthropic.com/a$b",
		"https://console.anthropic.com/a!b", "https://console.anthropic.com/a|b", "https://console.anthropic.com/a^b",
		"https://console.anthropic.com/a{b}", "https://console.anthropic.com/a[b]", "https://console.anthropic.com//evil.test:8443/x",
		// 非法字符恰好是最后一个字符（句末标点之外的）
		"https://console.anthropic.com/appeal*", "https://console.anthropic.com/appeal=", "https://console.anthropic.com/appeal#",
		"https://console.anthropic.com/appeal@", "https://console.anthropic.com/appeal$", "https://console.anthropic.com/appeal&",
		"https://console.anthropic.com/appeal(",
		// 令牌形态的路径（后面的令牌规则看不到被保留的链接，所以判定时就要挡掉）
		"https://platform.openai.com/sk-" + "placeholder0000000000",
		"https://platform.openai.com/account/api-keys/sk-" + "placeholder0000000000",
		"https://platform.openai.com/" + "eyJplaceholder0.placeholder0.placeholder0",
	} {
		input := "see " + link + " now"
		if got := Text(input); got != "see [redacted] now" {
			t.Errorf("non-public URL survived\n input: %q\n   got: %q", input, got)
		}
		if got, strict := Text(input), redactText(input, false); got != strict {
			t.Errorf("diverged from strict behaviour\n input: %q\n   got: %q\nstrict: %q", input, got, strict)
		}
	}
}

// 每个白名单主机的单点改动都不能被放行。
func TestMutatedVendorHostsAreRedacted(t *testing.T) {
	for _, host := range auditedPublicVendorHosts {
		variants := []string{
			"x" + host, host + "x", "x." + host, host + ".evil.test", "sub." + host, "-" + host, host + "-x",
			strings.Replace(host, ".", "-", 1), strings.Replace(host, ".", "", 1), host[1:], host[:len(host)-1],
			"evil.test/" + host, host + "@evil.test", "evil.test@" + host, host + ":8443", host + "?x=1", host + "#x",
			host + "%2eevil.test", strings.Replace(host, "i", "\u0130", 1), strings.Replace(host, "s", "\u017f", 1),
			strings.Replace(host, "o", "\u043e", 1), strings.Replace(host, "a", "\u0430", 1),
		}
		for _, variant := range variants {
			if _, isHost := publicVendorHosts[variant]; isHost {
				continue // 单点改动恰好撞上另一个白名单主机
			}
			input := "see https://" + variant + "/appeal now"
			if got := Text(input); got != "see [redacted] now" {
				t.Errorf("look-alike survived\n input: %q\n   got: %q", input, got)
			}
		}
	}
}

// assignmentPattern 仍然优先：值在 base_url= / upstream_url: 等赋值里就整体脱敏，
// 结果与旧行为逐字一致（含句末标点被链接吞掉后赋值值一直延伸到行尾的形状）。
func TestTextAssignmentsStillWinOverPublicURLs(t *testing.T) {
	for _, input := range []string{
		"base_url=https://console.anthropic.com/x",
		"upstream_url: https://console.anthropic.com/x",
		"proxy_url='https://console.anthropic.com/x'",
		"endpoint = https://console.anthropic.com/x",
		`{"base_url":"https://console.anthropic.com/x"}`,
		"api_key=https://console.anthropic.com/x",
		"Authorization: https://console.anthropic.com/x",
		"base_url=" + docsURL + ".",
		"token: https://console.anthropic.com/x, docs " + appealURL,
		"Incorrect API key provided: https://console.anthropic.com/x. Next",
	} {
		got, strict := Text(input), redactText(input, false)
		if got != strict || strings.Contains(got, "anthropic.com") {
			t.Errorf("assignment must win\n input: %q\n   got: %q\nstrict: %q", input, got, strict)
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"base_url=https://console.anthropic.com/x", "base_url=[redacted]"},
		{"upstream_url: https://console.anthropic.com/x", "upstream_url: [redacted]"},
		{"proxy_url='https://console.anthropic.com/x'", "proxy_url=[redacted]"},
		{`{"base_url":"https://console.anthropic.com/x"}`, `{"base_url":[redacted]}`},
		// 其他行 / 其他位置的公开链接不受影响。
		{"base_url=https://console.anthropic.com/x\nappeal at " + appealURL, "base_url=[redacted]\nappeal at " + appealURL},
		{"base_url=" + appealURL + "\nsee " + appealURL, "base_url=[redacted]\nsee " + appealURL},
	} {
		if got := Text(tc.input); got != tc.want {
			t.Errorf("input: %q\n   got: %q\n  want: %q", tc.input, got, tc.want)
		}
	}
}

func TestTextPublicURLNextToPrivateData(t *testing.T) {
	token := "sk-" + "proj-********abcd"
	for _, tc := range []struct{ name, input, want string }{
		{
			"relay url and bearer token",
			"POST https://relay.example.test/v1/messages failed: Bearer synthetic-token-value; read " + docsURL + ".",
			"POST [redacted] failed: [redacted]; read " + docsURL + ".",
		},
		{
			"openai incorrect key",
			"Incorrect API key provided: " + token + ". You can find your API key at https://platform.openai.com/account/api-keys.",
			"Incorrect API key provided: [redacted] You can find your API key at https://platform.openai.com/account/api-keys.",
		},
		{
			"private ip and email",
			"dial tcp 203.0.113.7:8443 refused, mail ops@relay.example.test, appeal at " + appealURL,
			"dial tcp [redacted] refused, mail [redacted], appeal at " + appealURL,
		},
	} {
		if got := Text(tc.input); got != tc.want {
			t.Errorf("%s\n input: %q\n   got: %q\n  want: %q", tc.name, tc.input, got, tc.want)
		}
	}
}

func TestTextPublicURLEscapedForms(t *testing.T) {
	for _, input := range []string{
		`https:\/\/console.anthropic.com\/appeal`,
		"https&#58;&#47;&#47;console.anthropic.com&#47;appeal",
		"https&amp;#58;&amp;#47;&amp;#47;console.anthropic.com&amp;#47;appeal",
		`https:\u002f\u002fconsole.anthropic.com\u002fappeal`,
		`appeal at https:\/\/console.anthropic.com\/appeal.`,
	} {
		if got := Text(input); got != input {
			t.Errorf("escaped public URL changed\n input: %q\n   got: %q", input, got)
		}
	}
	// 其他位置要脱敏时返回解码后的形态（与旧行为一致），公开链接照常保留。
	if got, want := Text(`https:\/\/console.anthropic.com\/appeal and https:\/\/relay.example.test\/v1`), "https://console.anthropic.com/appeal and [redacted]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	input := `{"error":{"message":"appeal at https:\/\/console.anthropic.com\/appeal"}}`
	if got := string(JSON([]byte(input))); got != input {
		t.Errorf("JSON-escaped public URL changed: %q", got)
	}
}

func TestTextDuplicateAndManyPublicURLs(t *testing.T) {
	twice := "see " + appealURL + " and again " + appealURL
	if got := Text(twice); got != twice {
		t.Errorf("duplicate URLs changed: %q", got)
	}
	// 一份被赋值吞掉，另一份仍然原样换回。
	if got, want := Text("base_url="+appealURL+"\nsee "+appealURL), "base_url=[redacted]\nsee "+appealURL; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	var in, want []string
	for i := 0; i < maxKeptURLs+8; i++ {
		link := "https://docs.anthropic.com/p" + strconv.Itoa(i)
		in = append(in, link)
		if i < maxKeptURLs {
			want = append(want, link)
		} else {
			want = append(want, redacted)
		}
	}
	if got := Text(strings.Join(in, " ")); got != strings.Join(want, " ") {
		t.Errorf("per-call cap not applied:\n got: %q\nwant: %q", got, strings.Join(want, " "))
	}
}

func TestTextIgnoresPublicURLsWhenInputCarriesPlaceholders(t *testing.T) {
	for _, r := range []rune{urlPlaceholderBase, urlPlaceholderBase + maxKeptURLs - 1} {
		marker := string(r)
		for _, input := range []string{marker + " " + appealURL, appealURL + " " + marker, marker + " " + appealURL + " " + marker} {
			want := strings.ReplaceAll(input, appealURL, redacted)
			if got := Text(input); got != want {
				t.Errorf("placeholder-bearing input must redact every URL\n input: %q\n   got: %q\n  want: %q", input, got, want)
			}
		}
	}
	// 解码出来的占位符字符同样算数（HTML 实体 / \u 转义）。
	for _, input := range []string{"&#xE000; " + appealURL, `\ue000 ` + appealURL, "&#57344; " + appealURL} {
		if got, want := Text(input), "\uE000 [redacted]"; got != want {
			t.Errorf("decoded placeholder not honoured\n input: %q\n   got: %q\n  want: %q", input, got, want)
		}
	}
	// 区间之外的私有区字符没有影响。
	for _, r := range []rune{urlPlaceholderBase - 1, urlPlaceholderBase + maxKeptURLs} {
		input := string(r) + " " + appealURL
		if got := Text(input); got != input {
			t.Errorf("rune %U outside the placeholder range changed behaviour: %q", r, got)
		}
	}
}

func TestTextOversizeInputStillFailsClosed(t *testing.T) {
	if got := Text(appealURL + " " + strings.Repeat("x", maxInputBytes)); got != redacted {
		t.Fatalf("oversize input must be redacted as a whole, got %d bytes", len(got))
	}
	if got := string(JSON([]byte(appealURL + strings.Repeat(" ", maxInputBytes)))); got != fallbackJSON {
		t.Fatalf("oversize JSON must fall back, got %q", got)
	}
	if testing.Short() {
		return
	}
	// 恰好等于上限的输入照常处理：公开链接保留，整串原样返回。
	atLimit := appealURL + " " + strings.Repeat("x", maxInputBytes-len(appealURL)-1)
	if got := Text(atLimit); got != atLimit {
		t.Fatalf("input of exactly maxInputBytes must be processed normally, got %d bytes", len(got))
	}
}

func TestJSONKeepsOrgOnHoldDocumentByteForByte(t *testing.T) {
	for _, status := range []int{0, 400, 403} {
		if got := string(JSONWithStatus([]byte(orgOnHoldDocument), status)); got != orgOnHoldDocument {
			t.Errorf("status %d changed the document:\n got: %s", status, got)
		}
	}
	if got := string(JSON([]byte(orgOnHoldDocument))); got != orgOnHoldDocument {
		t.Errorf("JSON changed the document:\n got: %s", got)
	}
}

func TestSSEKeepsOrgOnHoldDocumentByteForByte(t *testing.T) {
	named := "event: error\ndata: " + orgOnHoldDocument + "\n\n"
	dataOnly := "data: " + orgOnHoldDocument + "\n\n"
	for name, got := range map[string]string{
		"SSE":              SSE(named),
		"ErrorSSE":         ErrorSSE(named),
		"ErrorSSEStatus":   ErrorSSEWithStatus(named, 400),
		"ErrorSSEDataOnly": ErrorSSEWithStatus(dataOnly, 400),
	} {
		want := named
		if name == "ErrorSSEDataOnly" {
			want = dataOnly
		}
		if got != want {
			t.Errorf("%s changed the stream:\n got: %q", name, got)
		}
	}
}

func TestSSEErrorKeepsPublicURLAndRedactsRelayURL(t *testing.T) {
	input := "event: error\ndata: {\"error\":{\"message\":\"POST https://relay.example.test/v1 failed; appeal at " + appealURL + "\"}}\n\n"
	for name, got := range map[string]string{"SSE": SSE(input), "ErrorSSE": ErrorSSEWithStatus(input, 400)} {
		if !strings.Contains(got, appealURL) || strings.Contains(got, "relay.example.test") || !strings.Contains(got, "[redacted]") {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestJSONKeepsPublicURLsButKeyBasedRedactionStaysAuthoritative(t *testing.T) {
	input := `{"error":{"type":"invalid_request_error","message":"appeal at ` + appealURL + `",` +
		`"appeal_url":"` + appealURL + `","documentation_url":"` + docsURL + `",` +
		`"url":"` + appealURL + `","host":"console.anthropic.com","endpoint":"` + appealURL + `",` +
		`"base_url":"` + appealURL + `","upstream_url":"` + appealURL + `","proxy_url":"` + appealURL + `",` +
		`"URL":"` + appealURL + `","Base-Url":"` + appealURL + `","End Point":"` + appealURL + `",` +
		`"details":[{"help_url":"https://help.openai.com/en/articles/1","upstream":"` + appealURL + `"}]}}`
	var got struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(JSONWithStatus([]byte(input), 400), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"message", "appeal_url", "documentation_url"} {
		value, _ := got.Error[key].(string)
		if !strings.Contains(value, "https://") || strings.Contains(value, redacted) {
			t.Errorf("%s lost its public URL: %q", key, value)
		}
	}
	for _, key := range []string{"url", "host", "endpoint", "base_url", "upstream_url", "proxy_url", "URL", "Base-Url", "End Point"} {
		if got.Error[key] != redacted {
			t.Errorf("private key %q must stay redacted even for a public URL, got %v", key, got.Error[key])
		}
	}
	details, _ := got.Error["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("details lost: %v", got.Error["details"])
	}
	detail, _ := details[0].(map[string]any)
	if detail["help_url"] != "https://help.openai.com/en/articles/1" || detail["upstream"] != redacted {
		t.Errorf("nested fields wrong: %v", detail)
	}
}

func TestJSONRelayFingerprintStrippedButPublicURLKept(t *testing.T) {
	input := `{"error":{"type":"new_api_error","message":"appeal at ` + appealURL + ` (request id: Synthetic123)"}}`
	want := `{"error":{"message":"appeal at ` + appealURL + `","type":"invalid_request_error"}}`
	if got := string(JSONWithStatus([]byte(input), 400)); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

// ---- 属性 / 模糊测试：独立于实现的判定 + 与“全部脱敏”路径的差分 ----

const oracleTrailing = ".,;:!?)]}`\"'"

var oracleURL = regexp.MustCompile(`^https://([A-Za-z0-9.-]+)(/[A-Za-z0-9._~/%+-]*)?$`)

// oracleKeeps 按规则文字直接判定，不复用实现里的切分逻辑；主机段只含 ASCII，
// 所以这里用 strings.ToLower 是安全的。
func oracleKeeps(link string) bool {
	m := oracleURL.FindStringSubmatch(link)
	if m == nil || !slices.Contains(auditedPublicVendorHosts, strings.ToLower(m[1])) {
		return false
	}
	return !tokenPattern.MatchString(m[2])
}

func checkPublicURLInvariants(t *testing.T, s string) {
	t.Helper()
	got, strict := Text(s), redactText(s, false)
	decoded, ok := decodeDiagnosticText(s)
	if len(s) > maxInputBytes || !ok {
		if got != redacted || strict != redacted {
			t.Fatalf("over-limit input must fail closed: %q", got)
		}
		return
	}
	if hasURLPlaceholder(decoded) {
		if got != strict {
			t.Fatalf("placeholder-bearing input must take the strict path\n input: %q\n   got: %q\nstrict: %q", s, got, strict)
		}
		return
	}
	if hasURLPlaceholder(got) {
		t.Fatalf("placeholder leaked into the output\n input: %q\n   got: %q", s, got)
	}

	// 1. 期望输出 = 把放行的链接换成测试自己的占位符后走“全部脱敏”路径，再换回。
	pre := privateKeyPattern.ReplaceAllString(decoded, redacted)
	var kept []string
	var masked strings.Builder
	last := 0
	for _, loc := range urlPattern.FindAllStringIndex(pre, -1) {
		match := pre[loc[0]:loc[1]]
		if len(kept) >= maxKeptURLs || !oracleKeeps(strings.TrimRight(match, oracleTrailing)) {
			continue
		}
		_, _ = masked.WriteString(pre[last:loc[0]])
		_, _ = masked.WriteRune(rune(0xF000 + len(kept)))
		kept = append(kept, match)
		last = loc[1]
	}
	_, _ = masked.WriteString(pre[last:])
	want := redactText(masked.String(), false)
	for i, match := range kept {
		want = strings.ReplaceAll(want, string(rune(0xF000+i)), match)
	}
	if want == decoded {
		want = s
	}
	if got != want {
		t.Fatalf("output differs from the oracle\n input: %q\n   got: %q\n  want: %q", s, got, want)
	}

	// 2. 差分：把幸存的放行链接换回 [redacted]，必须得到旧行为的输出。
	norm, strictDecoded := got, strict
	if got == s {
		norm = decoded
	}
	if strict == s {
		strictDecoded = decoded
	}
	byLength := slices.Clone(kept)
	sort.Slice(byLength, func(i, j int) bool { return len(byLength[i]) > len(byLength[j]) })
	for _, match := range byLength {
		norm = strings.ReplaceAll(norm, match, redacted)
	}
	if norm != strictDecoded {
		t.Fatalf("not strict-equivalent apart from kept URLs\n input: %q\n   got: %q\n  norm: %q\nstrict: %q", s, got, norm, strictDecoded)
	}

	// 3. 没有任何新增泄漏：放行链接之外，不放行的链接出现的次数不能比旧行为多。
	for _, loc := range urlPattern.FindAllStringIndex(pre, -1) {
		match := pre[loc[0]:loc[1]]
		if !oracleKeeps(strings.TrimRight(match, oracleTrailing)) && strings.Count(norm, match) > strings.Count(strictDecoded, match) {
			t.Fatalf("non-public URL %q leaked\n input: %q\n   got: %q", match, s, got)
		}
	}

	// 4. 二次处理（存储后的消息会再被用户视图处理一遍）结果不变。
	if again := Text(got); again != got && redactText(strict, false) == strict {
		t.Fatalf("not idempotent\n input: %q\n   got: %q\n again: %q", s, got, again)
	}
}

var propertyAtoms = []string{
	// 放行的链接
	appealURL, docsURL, "https://ai.google.dev", "https://www.openai.com/", "https://CONSOLE.anthropic.com/x", "https://status.openai.com/incidents/abc",
	// 近似 / 不放行的链接
	"http://console.anthropic.com/appeal", "HTTPS://console.anthropic.com/appeal", "https://console.anthropic.com:443/appeal",
	"https://console.anthropic.com/appeal?x=1", "https://console.anthropic.com/appeal#f", "https://u@console.anthropic.com/",
	"https://console.anthropic.com.evil.test/", "https://sub.console.anthropic.com/", "https://console.anthrop\u0130c.com/appeal",
	"https://api.openai.com/v1/responses", "https://relay.example.test/v1/messages", "https://203.0.113.9/x", "https://[2001:db8::9]/x",
	"https://console.anthropic.com/a:b", "https://console.anthropic.com/a@b", "https://platform.openai.com/k/sk-" + "placeholder00000000",
	// 凭据 / 赋值上下文
	"Bearer abcdef", "sk-" + "placeholder00000000", "base_url=", "upstream_url: ", `"endpoint":"`, "token=", "key: ", "api_key=",
	"Authorization: ", "account 12 (name)", "Incorrect API key provided: ", "proxy ",
	// 文字
	"see", "at", "error", "Reference:", "(request id: Synthetic1)", "x.y.z", "203.0.113.4", "user@host.example", "dead:beef", "::1",
	// 标点与分隔
	" ", " ", "\n", "\t", ".", ",", ";", ":", "!", "?", ")", "(", "]", "[", "}", "{", "`", `"`, "'", "<", ">", `\`, "/", "@", "#", "&", "=", "%", "-", "_", "+", "*",
	// 转义
	`\/`, "&#47;", "&amp;", `\u002f`, `\u0026`, "&#xE000;", `\ue001`,
	// 怪字符
	"\u0130", "\u212a", "\u017f", "\u00e9", "\u4e2d", "\u200b", "\uE000", "\uE01f", "\uE020", "\xff", "\xee\x80",
}

var propertySeparators = []string{"", "", " ", " ", "\n", ", ", "; ", ". "}

func randomDiagnostic(r *rand.Rand) string {
	var b strings.Builder
	for n := 1 + r.Intn(7); n > 0; n-- {
		atom := propertyAtoms[r.Intn(len(propertyAtoms))]
		if r.Intn(8) == 0 {
			atom = strings.ToUpper(atom)
		}
		_, _ = b.WriteString(atom)
		_, _ = b.WriteString(propertySeparators[r.Intn(len(propertySeparators))])
	}
	return b.String()
}

func TestTextPublicURLProperty(t *testing.T) {
	r := rand.New(rand.NewSource(20261126))
	for i := 0; i < 6000; i++ {
		checkPublicURLInvariants(t, randomDiagnostic(r))
	}
	for _, s := range []string{orgOnHoldDocument, "", appealURL, docsURL + ".", strings.Repeat(appealURL+" ", 3*maxKeptURLs)} {
		checkPublicURLInvariants(t, s)
	}
}

func FuzzTextPublicURLs(f *testing.F) {
	for _, seed := range propertyAtoms {
		f.Add(seed)
		f.Add("see " + seed + " now")
	}
	for _, seed := range []string{orgOnHoldDocument, "base_url=" + appealURL + ", " + docsURL + ".", "\uE000" + appealURL} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		checkPublicURLInvariants(t, s)
	})
}
