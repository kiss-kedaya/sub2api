package service

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Regression tests for the findings of the adversarial audit of the top-level
// index (WebSocket contexts, malformed arrays, bounded work, strictness edges).

func topPaddedBody(core string) []byte {
	return []byte(`{"pad":"` + strings.Repeat("x", int(bodyTopDefaultMinBytes)+1000) + `",` + core)
}

// A WebSocket connection's gin context lives as long as the connection: the memo
// must not be created there, or it would pin two request bodies for all of it.
func TestRequestBodyTopSkipsWebsocketAndRequestlessContexts(t *testing.T) {
	big := topPaddedBody(`"model":"a","stream":true}`)

	ws := newTopTestContext()
	ws.Request.Header.Set("Connection", "Upgrade")
	ws.Request.Header.Set("Upgrade", "websocket")
	require.True(t, ws.IsWebsocket())
	top := RequestBodyTop(ws, big)
	require.Nil(t, top.idx, "a WebSocket context gets no index")
	_ = RequestBodyTop(ws, append([]byte(nil), big...)) // a second body: still nothing kept
	_, kept := ws.Get(bodyTopMemoKey)
	require.False(t, kept, "a WebSocket context must not hold the memo")
	require.Equal(t, "a", top.Get("model").String(), "it still answers, through gjson")
	require.True(t, top.Get("stream").Bool())
	require.True(t, top.Valid())

	// Mixed-case header values are still an upgrade.
	ws2 := newTopTestContext()
	ws2.Request.Header.Set("Connection", "keep-alive, Upgrade")
	ws2.Request.Header.Set("Upgrade", "WebSocket")
	require.True(t, ws2.IsWebsocket())
	require.Nil(t, RequestBodyTop(ws2, big).idx)

	// No request at all: gin's IsWebsocket would dereference it, the check must come first.
	bare, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, bare.Request)
	require.Nil(t, RequestBodyTop(bare, big).idx)
	_, kept = bare.Get(bodyTopMemoKey)
	require.False(t, kept)

	// Plain HTTP, including a keep-alive connection and a non-websocket Upgrade, is indexed.
	for _, hdr := range []map[string]string{nil, {"Connection": "keep-alive"}, {"Connection": "Upgrade", "Upgrade": "h2c"}} {
		c := newTopTestContext()
		for k, v := range hdr {
			c.Request.Header.Set(k, v)
		}
		require.False(t, c.IsWebsocket())
		require.NotNil(t, RequestBodyTop(c, big).idx, "headers %v", hdr)
		_, kept = c.Get(bodyTopMemoKey)
		require.True(t, kept)
	}
}

// gjson walks an array it has to look into with its own bracket matching, which on
// malformed text runs past the extent the squash rule gives the array and swallows the
// members after it. A two-key path therefore leaves an array under its first key to
// gjson. The first three inputs and the reverse-direction ones are the audit's.
func TestBodyTopTwoKeyPathDeclinesArrays(t *testing.T) {
	cases := []struct{ name, core, path string }{
		{"effort after [[]}", `"reasoning":[[]},"reasoning":{"effort":"high"}}`, "reasoning.effort"},
		{"maxOutputTokens after [[]}", `"generationConfig":[[]},"generationConfig":{"maxOutputTokens":9999999}}`, "generationConfig.maxOutputTokens"},
		{"output_config after [}", `"output_config":[},"output_config":{"effort":"max"}}`, "output_config.effort"},
		{"reverse [(]", `"reasoning":[(],"reasoning":{"effort":"x"}}}`, "reasoning.effort"},
		{"reverse [(] trailing space", `"reasoning":[(],"reasoning":{"effort":"x"}}} `, "reasoning.effort"},
		{"a.x after [(]", `"a":[(],"stream":true}}`, "a.x"},
		{"well-formed array first", `"reasoning":[1,2],"reasoning":{"effort":"y"}}`, "reasoning.effort"},
		{"empty array first", `"reasoning":[],"reasoning":{"effort":"z"}}`, "reasoning.effort"},
		{"array after an object without the key", `"reasoning":{"p":1},"reasoning":[(],"reasoning":{"effort":"w"}}}`, "reasoning.effort"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := topPaddedBody(tc.core)
			_, handled := (&bodyTopIndex{body: body}).get(tc.path)
			require.False(t, handled, "the index must hand this to gjson")
			require.Empty(t, topSamePath(gjson.GetBytes(body, tc.path), RequestBodyTop(newTopTestContext(), body).Get(tc.path)))
			small := []byte("{" + tc.core)
			require.Empty(t, topSamePath(gjson.GetBytes(small, tc.path), BodyTop{body: small, idx: &bodyTopIndex{body: small}}.Get(tc.path)))
		})
	}

	// A one-key path never looks inside, so an array there is squashed exactly as gjson
	// squashes it, malformed or not.
	for _, core := range []string{`"a":[(],"stream":true}}`, `"a":[[]},"stream":true}`, `"a":[1,2],"stream":true}`} {
		body := topPaddedBody(core)
		idx := &bodyTopIndex{body: body}
		got, handled := idx.get("stream")
		if handled {
			require.Empty(t, topSamePath(gjson.GetBytes(body, "stream"), got), core)
		}
		require.Empty(t, topSamePath(gjson.GetBytes(body, "stream"), BodyTop{body: body, idx: idx}.Get("stream")), core)
	}
}

// A lookup enters (validates and searches) at most bodyTopMaxNestedEntries members
// that share the first key and hands the rest to gjson, so its work stays within a
// small constant of gjson's on any input.
func TestBodyTopNestedEntriesAreBounded(t *testing.T) {
	// The bound is what keeps a lookup within a small constant of gjson's work; a
	// large one would let the audit's 1000-object body cost many times gjson's again.
	require.LessOrEqual(t, bodyTopMaxNestedEntries, 16)
	require.GreaterOrEqual(t, bodyTopMaxNestedEntries, 2, "a request with duplicated reasoning objects must still be answered")
	build := func(without int) []byte {
		var sb strings.Builder
		for i := 0; i < without; i++ {
			_, _ = sb.WriteString(`"reasoning":{"p":"y"},`)
		}
		_, _ = sb.WriteString(`"reasoning":{"effort":"z"}}`)
		return topPaddedBody(sb.String())
	}
	for without := 0; without <= bodyTopMaxNestedEntries+3; without++ {
		body := build(without)
		idx := &bodyTopIndex{body: body}
		got, handled := idx.get("reasoning.effort")
		entered := without + 1 // the answering object is entered last
		require.Equal(t, entered <= bodyTopMaxNestedEntries, handled, "%d objects without the key before the one that has it", without)
		if handled {
			require.Equal(t, "z", got.String())
		}
		require.Equal(t, "z", BodyTop{body: body, idx: idx}.Get("reasoning.effort").String(), "always the right answer")
	}

	// The audit's input: 1000 same-named 4 KB objects, none with the key.
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		_, _ = sb.WriteString(`"reasoning":{"p":"` + strings.Repeat("y", 4000) + `"},`)
	}
	_, _ = sb.WriteString(`"model":"m"}`)
	adv := topPaddedBody(sb.String())
	_, handled := (&bodyTopIndex{body: adv}).get("reasoning.effort")
	require.False(t, handled, "bounded work: the 9th object is gjson's")
	for i := 0; i < 3; i++ {
		require.Empty(t, topSamePath(gjson.GetBytes(adv, "reasoning.effort"), RequestBodyTop(newTopTestContext(), adv).Get("reasoning.effort")))
	}
}

// BenchmarkAdversarialNestedRepeat is the audit's worst case: 1000 same-named 4 KB
// objects, the same two-key question asked 10 times. Compare the two sub-benchmarks:
// the index side pays at most bodyTopMaxNestedEntries small entries per question on
// top of the gjson search it hands the question to.
func BenchmarkAdversarialNestedRepeat(b *testing.B) {
	var sb strings.Builder
	_, _ = sb.WriteString("{")
	for i := 0; i < 1000; i++ {
		_, _ = sb.WriteString(`"reasoning":{"p":"` + strings.Repeat("y", 4000) + `"},`)
	}
	_, _ = sb.WriteString(`"model":"m"}`)
	body := []byte(sb.String())
	b.Run(fmt.Sprintf("gjson/%dKB/x10", len(body)>>10), func(b *testing.B) {
		b.SetBytes(int64(len(body)) * 10)
		for i := 0; i < b.N; i++ {
			for q := 0; q < 10; q++ {
				benchSink = gjson.GetBytes(body, "reasoning.effort")
			}
		}
	})
	b.Run(fmt.Sprintf("index/%dKB/x10", len(body)>>10), func(b *testing.B) {
		c := newTopTestContext()
		b.SetBytes(int64(len(body)) * 10)
		for i := 0; i < b.N; i++ {
			c.Keys = nil
			for q := 0; q < 10; q++ {
				benchSink = RequestBodyTop(c, body).Get("reasoning.effort")
			}
		}
	})
}

// Where the index must decline rather than answer. The answers agree with gjson
// whether or not the index handles them, so only the white-box "handled" flag can
// tell a strict scan from a lenient one; these cases pin the strict one.
func TestBodyTopStrictnessBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		path    string
		handled bool
	}{
		{"comma before the first member", `{,"a":1}`, "a", false},
		{"comma before the first member, absent key", `{,"a":1}`, "b", false},
		{"two commas before a member", `{"a":1,,"b":2}`, "b", false},
		{"trailing comma, member before it still answers", `{"a":1,}`, "a", true},
		{"trailing comma, absent key reaches it", `{"a":1,}`, "b", false},
		{"garbage after the object, absent key reaches it", `{"a":1} x`, "b", false},
		{"garbage after the object, answered earlier", `{"a":1} x`, "a", true},
		{"second object after the first, absent key", `{"a":1}{"b":2}`, "b", false},
		{"whitespace after the object is fine", "{\"a\":1} \n\t\r", "b", true},
		{"missing comma", `{"a":1 "b":2}`, "b", false},
		{"missing colon", `{"a" 1}`, "a", false},
		{"unterminated after a number", `{"a":1`, "a", false},
		{"unterminated after a string", `{"a":"x"`, "a", true},
		{"unterminated object, absent key", `{"a":"x"`, "b", false},
		{"empty object", `{}`, "a", true},
		{"empty object with whitespace", " { \n} ", "a", true},
		{"not an object", `[{"a":1}]`, "a", false},
		{"bom before the object", "\xef\xbb\xbf" + `{"a":1}`, "a", false},
		{"key spelled with an escape", strings.ReplaceAll("{\"@u0061\":1}", "@", "\\"), "a", false},
		{"non-JSON number", `{"a":01}`, "a", false},
		{"non-JSON literal", `{"a":nul}`, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			idx := &bodyTopIndex{body: body}
			_, handled := idx.get(tc.path)
			require.Equal(t, tc.handled, handled)
			require.Empty(t, topSamePath(gjson.GetBytes(body, tc.path), BodyTop{body: body, idx: idx}.Get(tc.path)))
		})
	}
}

// The member table stops at 1024 members: bodies at and just past the limit. The
// number is written out here on purpose, so that changing the cap (the code, not the
// test) turns this red until the documented limit is changed with it.
func TestBodyTopMemberCapBoundary(t *testing.T) {
	const limit = 1024
	require.Equal(t, limit, bodyTopMaxMembers, "the member cap changed: update the file comment and this test together")
	// members "k0".."k<n-1>", the one at target (if >= 0) replaced by service_tier.
	build := func(n, target int) []byte {
		var sb strings.Builder
		_, _ = sb.WriteString("{")
		for i := 0; i < n; i++ {
			if i > 0 {
				_, _ = sb.WriteString(",")
			}
			if i == target {
				_, _ = sb.WriteString(`"service_tier":"flex"`)
			} else {
				_, _ = fmt.Fprintf(&sb, `"k%d":%d`, i, i)
			}
		}
		_, _ = sb.WriteString("}")
		return []byte(sb.String())
	}
	for _, n := range []int{limit - 24, limit - 1, limit, limit + 1, limit + 2} {
		for _, target := range []int{-1, 0, limit - 1, n - 1} {
			if target >= n {
				continue
			}
			body := build(n, target)
			idx := &bodyTopIndex{body: body}
			got, handled := idx.get("service_tier")
			// Reaching member number limit+1 (index limit) is what fails; an answer
			// found before it, or an end reached before it, stands.
			reached := n // members scanned when the key is absent
			if target >= 0 {
				reached = target + 1
			}
			require.Equal(t, reached <= limit, handled, "n=%d target=%d", n, target)
			if handled && target >= 0 {
				require.Equal(t, "flex", got.String())
			}
			require.Empty(t, topSamePath(gjson.GetBytes(body, "service_tier"), BodyTop{body: body, idx: idx}.Get("service_tier")), "n=%d target=%d", n, target)
		}
	}
}
