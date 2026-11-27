package service

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
	"weak"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// legacyDeclaredClientFunctionToolNames 是优化前的实现，只作对照（oracle）：
// gjson.ParseBytes 把整份请求体复制成 string，map key 是该 string 的子串。
func legacyDeclaredClientFunctionToolNames(body []byte) map[string]struct{} {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil
	}
	names := make(map[string]struct{}, 4)
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		names[name] = struct{}{}
	}
	parsed := gjson.ParseBytes(body)
	parsed.Get("tools").ForEach(func(_, tool gjson.Result) bool {
		if !tool.IsObject() {
			return true
		}
		kind := strings.ToLower(strings.TrimSpace(tool.Get("type").String()))
		switch kind {
		case "", "function", "custom":
		default:
			return true
		}
		add(tool.Get("name").String())
		add(tool.Get("function.name").String())
		return true
	})
	parsed.Get("functions").ForEach(func(_, fn gjson.Result) bool {
		add(fn.Get("name").String())
		add(fn.Get("function.name").String())
		return true
	})
	if len(names) == 0 {
		return nil
	}
	return names
}

func TestDeclaredClientFunctionToolNamesMatchesLegacy(t *testing.T) {
	fixed := []string{
		``,
		`null`,
		`123`,
		`"tools"`,
		`[{"tools":[{"name":"a"}]}]`,
		`{"tools":[{"name":"a"}]`,
		`{"tools":[{"name":"a"}]} trailing`,
		"\ufeff" + `{"tools":[{"name":"a"}]}`,
		` {"tools":[{"type":"function","name":"Get_Weather"}]} `,
		`{"tools":[{"type":"function","function":{"name":" Lookup "}}]}`,
		`{"functions":[{"name":"legacy_fn"},{"function":{"name":"nested_legacy"}}]}`,
		`{"tools":[{"type":"web_search_preview"},{"type":"web_search","name":"web_search"}]}`,
		`{"tools":[{"type":"Custom","name":"patch"},{"type":"","name":"typed_empty"},{"name":"untyped"}]}`,
		`{"tools":[{"name":"first"}],"tools":[{"name":"second"}]}`,
		`{"tools":[{"name":"escaped_key"}],"functions":[{"name":"escaped_inner"}]}`,
		`{"to` + `\u00` + `6fls":[{"name":"escaped_tools_key"}],"fun` + `\u00` + `63tions":[{"na` + `\u00` + `6De":"esc_value_key"}]}`,
		`{"input":[{"tools":[{"name":"nested_not_top_level"}]}],"tools":[{"name":"top_level"}]}`,
		`{"tools":{"a":{"name":"object_tools"}}}`,
		`{"tools":"web_search","functions":7}`,
		`{"tools":[1,null,"x",[{"name":"in_array"}],{"name":123},{"name":true},{"name":{"x":1}}]}`,
		`{"tools":[{"name":"Ünï-ÇÖDE"},{"name":"İSTANBUL"}]}`,
		`{"tools":[{"name":"a\"b"},{"name":"tab\there"}]}`,
		`{"tools":[{"name":"dup"},{"name":"DUP"},{"function":{"name":"dup"}}]}`,
		`{"stream":true,"tools":[],"functions":[]}`,
		`{"tools":[{"name":"  "}],"functions":[{"name":""}]}`,
	}
	for _, body := range fixed {
		require.Equal(t, legacyDeclaredClientFunctionToolNames([]byte(body)), declaredClientFunctionToolNames(nil, []byte(body)), "body: %q", body)
	}
}

func randomToolNamesBody(r *rand.Rand) string {
	ws := func() string { return []string{"", "", "", " ", "\n", "\t "}[r.Intn(6)] }
	name := func() string {
		switch r.Intn(8) {
		case 0:
			return `"Get_Weather"`
		case 1:
			return `" padded "`
		case 2:
			return `"web_search"`
		case 3:
			return `""`
		case 4:
			return `123`
		case 5:
			return `"name_` + fmt.Sprint(r.Intn(3)) + `"`
		default:
			return fmt.Sprintf(`"fn_%d"`, r.Intn(6))
		}
	}
	tool := func() string {
		switch r.Intn(10) {
		case 0:
			return `{"type":"function","name":` + name() + `}`
		case 1:
			return `{"type":"function","function":{"name":` + name() + `}}`
		case 2:
			return `{"type":"web_search_preview"}`
		case 3:
			return `{` + ws() + `"name":` + name() + ws() + `}`
		case 4:
			return `{"type":"custom","name":` + name() + `}`
		case 5:
			return `"just a string"`
		case 6:
			return `null`
		case 7:
			return `{"type":"Function","name":` + name() + `,"function":{"name":` + name() + `}}`
		case 8:
			return `[{"name":"inside_array"}]`
		default:
			return `{"type":"image_generation","name":` + name() + `}`
		}
	}
	list := func() string {
		switch r.Intn(6) {
		case 0:
			return `[]`
		case 1:
			return `"scalar"`
		case 2:
			return `{"k":` + tool() + `}`
		default:
			n := r.Intn(4) + 1
			parts := make([]string, n)
			for i := range parts {
				parts[i] = ws() + tool() + ws()
			}
			return `[` + strings.Join(parts, ",") + `]`
		}
	}
	members := []string{`"model":"m"`, `"stream":true`, `"input":[{"role":"user","tools":[{"name":"shadow"}]},` + tool() + `]`}
	for i, n := 0, r.Intn(3); i < n; i++ {
		members = append(members, `"tools":`+list())
	}
	if r.Intn(2) == 0 {
		members = append(members, `"functions":`+list())
	}
	if r.Intn(5) == 0 {
		members = append(members, `"tools":`+list())
	}
	r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	for i := range members {
		members[i] = ws() + members[i] + ws()
	}
	body := ws() + `{` + strings.Join(members, ",") + `}` + ws()
	if r.Intn(25) == 0 {
		body = body[:len(body)/2]
	}
	return body
}

func TestDeclaredClientFunctionToolNamesMatchesLegacyRandomized(t *testing.T) {
	r := rand.New(rand.NewSource(491))
	for i := 0; i < 4000; i++ {
		body := randomToolNamesBody(r)
		require.Equal(t, legacyDeclaredClientFunctionToolNames([]byte(body)), declaredClientFunctionToolNames(nil, []byte(body)), "body: %s", body)
	}
}

// 大请求体 + 少量工具：模拟 Codex 把整段历史放在 input 里。
func largeToolNamesBody(inputBytes int) []byte {
	var sb strings.Builder
	sb.Grow(inputBytes + 512)
	_, _ = sb.WriteString(`{"model":"m","stream":true,"input":[{"role":"user","content":"`)
	_, _ = sb.WriteString(strings.Repeat("x", inputBytes))
	_, _ = sb.WriteString(`"}],"tools":[{"type":"function","name":"get_weather"},{"type":"web_search_preview"},{"type":"function","function":{"name":"lookup_user"}}]}`)
	return []byte(sb.String())
}

func settledHeapAlloc() uint64 {
	var ms runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// retainedBytes 返回 build() 返回值仍存活时、其内部临时分配之外多留住的堆字节数。
// build 里构造的请求体在返回后不再被引用，所以只有返回值（map）能把它钉住。
func retainedBytes(build func() any) int64 {
	before := settledHeapAlloc()
	kept := build()
	after := settledHeapAlloc()
	runtime.KeepAlive(kept)
	return int64(after) - int64(before)
}

// 内存保留断言：map 活着时，请求体（及其任何复制品）必须能被回收。
// 旧实现的 key 是整份复制品的子串，会把 16MB 钉到请求结束。
func TestDeclaredClientFunctionToolNamesDoesNotRetainBody(t *testing.T) {
	const bodySize = 16 << 20
	measure := func(fn func([]byte) map[string]struct{}) int64 {
		return retainedBytes(func() any {
			names := fn(largeToolNamesBody(bodySize))
			require.Len(t, names, 2)
			return names
		})
	}

	// 先证明测量手法本身能抓到问题，否则下面的断言是空转。
	legacyRetained := measure(legacyDeclaredClientFunctionToolNames)
	require.Greater(t, legacyRetained, int64(bodySize)*3/4, "旧实现应当把整份复制品钉住")

	currentRetained := measure(func(body []byte) map[string]struct{} { return declaredClientFunctionToolNames(nil, body) })
	t.Logf("请求体 %d 字节；map 存活时多留住的堆: legacy=%d current=%d", bodySize, legacyRetained, currentRetained)
	require.Less(t, currentRetained, int64(bodySize)/8, "key 不应再引用请求体或其复制品")
}

// key 必须是独立字符串：零拷贝视图上的子串若直接入 map，会钉住 body，
// 且 body 之后被改写（例如就地编辑）时 key 会跟着变。
func TestDeclaredClientFunctionToolNamesKeysDoNotAliasBody(t *testing.T) {
	body := largeToolNamesBody(1 << 20)
	names := declaredClientFunctionToolNames(nil, body)
	require.Len(t, names, 2)

	base := uintptr(unsafe.Pointer(unsafe.SliceData(body)))
	end := base + uintptr(len(body))
	for name := range names {
		p := uintptr(unsafe.Pointer(unsafe.StringData(name)))
		require.False(t, p >= base && p < end, "key %q 指向请求体内部", name)
	}

	// 原地改写请求体后，key 内容不变。
	for i := range body {
		body[i] = 'Z'
	}
	require.Contains(t, names, "get_weather")
	require.Contains(t, names, "lookup_user")
}

// 请求体本身必须能在 map 仍存活时被 GC 回收（finalizer 触发）。
func TestDeclaredClientFunctionToolNamesBodyIsCollectable(t *testing.T) {
	collected := make(chan struct{})
	names := func() map[string]struct{} {
		body := largeToolNamesBody(4 << 20)
		runtime.SetFinalizer(unsafe.SliceData(body), func(*byte) { close(collected) })
		return declaredClientFunctionToolNames(nil, body)
	}()
	require.Len(t, names, 2)
	deadline := time.After(10 * time.Second)
	for {
		runtime.GC()
		select {
		case <-collected:
			runtime.KeepAlive(names)
			return
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("请求体仍被 map 引用，未能回收")
		}
	}
}

func BenchmarkDeclaredClientFunctionToolNames(b *testing.B) {
	for _, size := range []int{64 << 10, 2 << 20} {
		body := largeToolNamesBody(size)
		for _, impl := range []struct {
			name string
			fn   func([]byte) map[string]struct{}
		}{
			{"legacy", legacyDeclaredClientFunctionToolNames},
			{"current", func(body []byte) map[string]struct{} { return declaredClientFunctionToolNames(nil, body) }},
		} {
			b.Run(fmt.Sprintf("%s/body_%dKB", impl.name, size>>10), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for i := 0; i < b.N; i++ {
					if len(impl.fn(body)) != 2 {
						b.Fatal("unexpected tool names")
					}
				}
			})
		}
	}
}

func TestRememberOpenAINativeSearchSkipFunctionToolsDedupAndRelease(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	w := func() weak.Pointer[byte] {
		body := largeToolNamesBody(1 << 20)
		rememberOpenAINativeSearchSkipFunctionTools(c, body)
		skip := openAINativeSearchSkipFunctionTools(c)
		require.Contains(t, skip, "get_weather")
		require.Contains(t, skip, "lookup_user")

		// Calling again with the exact same slice hits the slice-identity dedup fast path.
		c.Set(openAINativeSearchSkipFunctionToolsContextKey, map[string]struct{}{"sentinel": {}})
		rememberOpenAINativeSearchSkipFunctionTools(c, body)
		require.Contains(t, openAINativeSearchSkipFunctionTools(c), "sentinel", "same slice must skip recomputation")
		return weak.Make(&body[0])
	}()

	if w.Value() == nil {
		t.Fatal("expected context to retain body before ReleaseRequestBodyReferences")
	}

	// Passing a distinct slice recomputes skip tools.
	other := []byte(`{"tools":[{"type":"function","name":"new_tool"}]}`)
	rememberOpenAINativeSearchSkipFunctionTools(c, other)
	require.Contains(t, openAINativeSearchSkipFunctionTools(c), "new_tool")

	ReleaseRequestBodyReferences(c)
	requireBodyCollected(t, w, "ReleaseRequestBodyReferences must release openAINativeSearchSkipFunctionToolsBodyKey")
	runtime.KeepAlive(c)
}
