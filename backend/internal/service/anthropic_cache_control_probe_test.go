package service

import (
	"bytes"
	"math/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// legacyRequestNeedsExtendedCacheTTLBeta and legacyRequestNeedsThinkingDisplayUpdatesBeta are the two
// predicates as they were before the walks: a substring pre-filter, then gjson lookups.
func legacyRequestNeedsExtendedCacheTTLBeta(body []byte) bool {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"`+cacheTTLTarget1h+`"`)) {
		return false
	}
	needed := false
	forEachCacheControl(body, func(cc gjson.Result) {
		if cc.Get("type").String() == "ephemeral" && cc.Get("ttl").String() == cacheTTLTarget1h {
			needed = true
		}
	})
	return needed
}

func legacyRequestNeedsThinkingDisplayUpdatesBeta(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	display := gjson.GetBytes(body, "thinking.display")
	if !display.Exists() || display.Type != gjson.String {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(display.String()), "updates")
}

func perf18RandCacheControl(rng *rand.Rand) string {
	typ := []string{`"ephemeral"`, `"ephemeral"`, `"ephemeral"`, `"Ephemeral"`, `"persistent"`, `1`, `null`, `{"a":1}`, `["ephemeral"]`, perf18Unesc(`"ephem§u0065ral"`), `" ephemeral"`}
	ttl := []string{`"1h"`, `"1h"`, `"1h"`, `"5m"`, `"1H"`, `"1h "`, `1`, `null`, `true`, perf18Unesc(`"1§u0068"`), `"1h","ttl":"5m"`, `{"x":"1h"}`}
	var members []string
	if rng.Intn(10) != 0 {
		members = append(members, `"type":`+typ[rng.Intn(len(typ))])
	}
	if rng.Intn(5) != 0 {
		members = append(members, `"ttl":`+ttl[rng.Intn(len(ttl))])
	}
	if rng.Intn(8) == 0 {
		members = append(members, `"type":`+typ[rng.Intn(len(typ))]) // duplicate: the first decides
	}
	if rng.Intn(10) == 0 {
		members = append(members, `"extra":{"ttl":"1h","type":"ephemeral"}`)
	}
	rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	cc := "{" + perf18RandWS(rng) + strings.Join(members, ","+perf18RandWS(rng)) + perf18RandWS(rng) + "}"
	switch rng.Intn(14) {
	case 0:
		return `"ephemeral"` // not an object
	case 1:
		return `[` + cc + `]`
	case 2:
		return `null`
	}
	return cc
}

func perf18RandBlock(rng *rand.Rand) string {
	cc := `"cache_control":` + perf18RandCacheControl(rng)
	switch rng.Intn(12) {
	case 0:
		return `"text"`
	case 1:
		return `5`
	case 2:
		return `[{` + cc + `}]` // an array element: has no members
	case 3: // cache_control only deeper down: not a block member
		return `{"type":"tool_use","input":{` + cc + `}}`
	case 4: // the first cache_control member decides
		return `{` + cc + `,"text":"x",` + perf18RandCacheControlMember(rng) + `}`
	case 5: // escaped spelling of the key (§ stands for a backslash)
		return perf18Unesc(`{"type":"text","cache§u005fcontrol":`) + perf18RandCacheControl(rng) + `}`
	case 6:
		return `{"type":"text","text":"no cache control here"}`
	default:
		return `{"type":"text","text":"x",` + cc + `}`
	}
}

func perf18RandCacheControlMember(rng *rand.Rand) string {
	return `"cache_control":` + perf18RandCacheControl(rng)
}

func perf18RandBlocks(rng *rand.Rand) string {
	switch rng.Intn(12) {
	case 0:
		return `"a string"`
	case 1:
		return `{"type":"text","cache_control":` + perf18RandCacheControl(rng) + `}`
	case 2:
		return `null`
	}
	n := rng.Intn(5)
	blocks := make([]string, n)
	for i := range blocks {
		blocks[i] = perf18RandBlock(rng)
	}
	return "[" + strings.Join(blocks, ","+perf18RandWS(rng)) + "]"
}

func perf18RandCacheBody(rng *rand.Rand) string {
	var members []string
	add := func(m string) { members = append(members, m) }
	add(`"model":"claude-opus-5-5"`)
	if rng.Intn(3) == 0 {
		add(`"cache_control":` + perf18RandCacheControl(rng))
	}
	if rng.Intn(10) == 0 {
		add(`"cache_control":` + perf18RandCacheControl(rng)) // twice: the first one decides
	}
	if rng.Intn(2) == 0 {
		add(`"system":` + perf18RandBlocks(rng))
	}
	if rng.Intn(14) == 0 {
		add(`"system":` + perf18RandBlocks(rng))
	}
	if rng.Intn(2) == 0 {
		add(`"tools":` + perf18RandBlocks(rng))
	}
	switch rng.Intn(8) {
	case 0:
	case 1:
		add(`"messages":{"content":[{"cache_control":{"type":"ephemeral","ttl":"1h"}}]}`) // not an array
	case 2:
		add(`"messages":"text"`)
	case 3:
		add(`"messages":[{"role":"user","content":"x"},{"role":"user","content":` + perf18RandBlocks(rng) + `}]`)
	case 4: // two messages members: the first decides
		add(`"messages":[]`)
		add(`"messages":[{"content":` + perf18RandBlocks(rng) + `}]`)
	default:
		n := rng.Intn(5)
		msgs := make([]string, n)
		for i := range msgs {
			switch rng.Intn(9) {
			case 0:
				msgs[i] = `"not an object"`
			case 1:
				msgs[i] = `[{"content":` + perf18RandBlocks(rng) + `}]`
			case 2: // content twice: the first one decides
				msgs[i] = `{"role":"user","content":"x","content":` + perf18RandBlocks(rng) + `}`
			case 3:
				msgs[i] = perf18Unesc(`{"role":"user","cont§u0065nt":`) + perf18RandBlocks(rng) + `}`
			case 4:
				msgs[i] = `{"role":"user","meta":{"content":` + perf18RandBlocks(rng) + `}}`
			default:
				msgs[i] = `{"role":"user","content":` + perf18RandBlocks(rng) + `}`
			}
		}
		add(`"messages":[` + strings.Join(msgs, ",") + `]`)
	}
	rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	var sb strings.Builder
	sb.WriteString("{" + perf18RandWS(rng))
	for i, m := range members {
		if i > 0 {
			sb.WriteString("," + perf18RandWS(rng))
		}
		sb.WriteString(m + perf18RandWS(rng))
	}
	sb.WriteString("}")
	body := sb.String()
	switch rng.Intn(50) {
	case 0:
		return `[` + body + `]`
	case 1:
		return body[:len(body)/2]
	case 2:
		return perf18Unesc(`{"cache§u005fcontrol":{"type":"ephemeral","ttl":"1h"}}`)
	}
	return body
}

func TestExtendedCacheTTLNeededFastMatchesGJSON(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	hits, misses, unknown := 0, 0, 0
	for iter := 0; iter < 60000; iter++ {
		body := []byte(perf18RandCacheBody(rng))
		want := legacyRequestNeedsExtendedCacheTTLBeta(body)
		// the public predicate (walk, then gjson for what the walk does not know)
		require.Equal(t, want, requestNeedsExtendedCacheTTLBeta(body), "body %q", body)
		// the walk on its own is exact wherever it answers
		if !bytes.Contains(body, []byte(`"1h"`)) {
			continue // the pre-filter answers these (including an escaped 1h, which it never counted)
		}
		needed, known := extendedCacheTTLNeededFast(body)
		if !known {
			unknown++
			continue
		}
		require.Equal(t, want, needed, "body %q", body)
		if want {
			hits++
		} else {
			misses++
		}
	}
	require.Greater(t, hits, 1500)
	require.Greater(t, misses, 1500)
	require.Less(t, unknown, hits+misses, "the walk has to answer most bodies by itself")
}

func TestThinkingDisplayUpdatesFastMatchesGJSON(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	thinkings := []string{
		`{"type":"enabled","display":"updates"}`, `{"display":"Updates"}`, `{"display":" UPDATES "}`, `{"display":"summarized"}`,
		`{"display":"omitted","display":"updates"}`, `{"display":null}`, `{"display":5}`, `{"display":["updates"]}`, `{"display":{"x":"updates"}}`,
		perf18Unesc(`{"display":"upd§u0061tes"}`), perf18Unesc(`{"§u0064isplay":"updates"}`), `{"display":"updates"`, `{"nested":{"display":"updates"}}`,
		`{"display": "updates" , "type" : "enabled"}`, `"updates"`, `5`, `true`, `null`, `[{"display":"updates"}]`, `[]`, `{}`, `{"display":"updátes"}`,
	}
	known, total := 0, 0
	for iter := 0; iter < 40000; iter++ {
		var members []string
		members = append(members, `"model":"m"`)
		for i, n := 0, rng.Intn(3); i < n; i++ {
			members = append(members, `"thinking":`+thinkings[rng.Intn(len(thinkings))])
		}
		if rng.Intn(3) == 0 {
			members = append(members, `"messages":[{"role":"user","content":"thinking display updates"}]`)
		}
		if rng.Intn(6) == 0 {
			members = append(members, perf18Unesc(`"§u006dessages":[]`))
		}
		rng.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
		body := []byte("{" + strings.Join(members, ",") + "}")
		if rng.Intn(40) == 0 {
			body = body[:len(body)/2]
		}
		want := legacyRequestNeedsThinkingDisplayUpdatesBeta(body)
		require.Equal(t, want, requestNeedsThinkingDisplayUpdatesBeta(body), "body %q", body)
		needed, ok := thinkingDisplayUpdatesFast(body)
		total++
		if ok {
			known++
			require.Equal(t, want, needed, "body %q", body)
		}
	}
	// about half of these bodies are deliberately awkward (repeated or array thinking members,
	// escapes), which the walk rightly hands to gjson; ordinary bodies are the check below
	require.Greater(t, known*100/total, 30)
	for _, sz := range perf18Sizes {
		ordinary := perf18AnthropicBody(sz.size, perf18Code, "")
		needed, ok := thinkingDisplayUpdatesFast(ordinary)
		require.True(t, ok, sz.name)
		require.False(t, needed, sz.name)
	}
	updates := []byte(`{"model":"m","thinking":{"type":"enabled","display":"updates"},"messages":[]}`)
	needed, ok := thinkingDisplayUpdatesFast(updates)
	require.True(t, ok)
	require.True(t, needed)
}

// legacy versions of the header helpers: the body predicate evaluated again inside ensure*.
func legacyApplyExtendedCacheTTLBetaHeader(h http.Header, body []byte) {
	if h == nil || !legacyRequestNeedsExtendedCacheTTLBeta(body) {
		return
	}
	current := strings.Join(anthropicBetaHeaderValues(h), ",")
	next := legacyEnsureExtendedCacheTTLBeta(current, body, nil)
	if next == current {
		return
	}
	deleteHeaderAllForms(h, "anthropic-beta")
	setHeaderRaw(h, "anthropic-beta", next)
}

func legacyEnsureExtendedCacheTTLBeta(betaHeader string, body []byte, drop map[string]struct{}) string {
	if !legacyRequestNeedsExtendedCacheTTLBeta(body) {
		return betaHeader
	}
	token := claude.BetaExtendedCacheTTL
	if containsBetaToken(betaHeader, token) {
		return betaHeader
	}
	if _, dropped := drop[token]; dropped {
		return betaHeader
	}
	if strings.TrimSpace(betaHeader) == "" {
		return token
	}
	return betaHeader + "," + token
}

func TestBetaHeaderHelpersMatchLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	headers := []string{"", " ", "oauth-2025-04-20", claude.BetaExtendedCacheTTL, "a," + claude.BetaExtendedCacheTTL + ",b", " " + claude.BetaExtendedCacheTTL + " ", "a,,b", "x"}
	bodies := []string{
		`{"system":[{"type":"text","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`,
		`{"system":[{"type":"text","cache_control":{"type":"ephemeral","ttl":"5m"}}]}`,
		`{"messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`,
		`{"thinking":{"type":"enabled","display":"updates"}}`, `{"thinking":{"display":"summarized"}}`, `{}`, ``,
	}
	for i := 0; i < 4000; i++ {
		bodies = append(bodies, perf18RandCacheBody(rng))
	}
	for _, body := range bodies {
		b := []byte(body)
		for _, header := range headers {
			for _, drop := range []map[string]struct{}{nil, {claude.BetaExtendedCacheTTL: {}}} {
				require.Equal(t, legacyEnsureExtendedCacheTTLBeta(header, b, drop), ensureExtendedCacheTTLBeta(header, b, drop), "header %q body %q", header, body)
			}
			want := http.Header{}
			got := http.Header{}
			for _, h := range []http.Header{want, got} {
				if header != "" {
					h["anthropic-beta"] = []string{header}
				}
			}
			legacyApplyExtendedCacheTTLBetaHeader(want, b)
			applyExtendedCacheTTLBetaHeader(got, b)
			require.Equal(t, want, got, "header %q body %q", header, body)
		}
	}
}

// The cache-control probes cost no allocation on an ordinary body; the gjson walk copies messages and
// allocates a Result per element.
func TestCacheControlProbesAllocateNothing(t *testing.T) {
	body := perf18AnthropicBody(150_000, perf18Code, `"cache_control":{"type":"ephemeral","ttl":"1h"}`)
	require.True(t, legacyRequestNeedsExtendedCacheTTLBeta(body))
	require.Greater(t, testing.AllocsPerRun(5, func() { legacyRequestNeedsExtendedCacheTTLBeta(body) }), 0.0, "control")
	require.Zero(t, testing.AllocsPerRun(10, func() {
		if !requestNeedsExtendedCacheTTLBeta(body) {
			t.Fatal("expected the 1h beta to be needed")
		}
	}))
	plain := perf18AnthropicBody(150_000, perf18Code, "")
	require.False(t, requestNeedsThinkingDisplayUpdatesBeta(plain))
	require.Zero(t, testing.AllocsPerRun(10, func() { requestNeedsThinkingDisplayUpdatesBeta(plain) }))
}
