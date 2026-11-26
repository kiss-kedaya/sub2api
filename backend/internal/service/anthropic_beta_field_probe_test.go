//go:build unit

package service

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// legacySanitizeAnthropicBodyForBetaTokens / legacySanitizeAnthropicFallbackFields are the two
// sanitizers exactly as they were before the field probe: one gjson existence lookup per field,
// the header consulted only for a field that exists.
func legacySanitizeAnthropicBodyForBetaTokens(body []byte, header string) ([]byte, bool) {
	if len(body) == 0 {
		return body, false
	}
	changed := false
	if b, deleted := stripAnthropicBodyFieldUnlessBeta(body, "context_management", header,
		anthropicBetaContextManagementToken, anthropicBetaCompactToken); deleted {
		body, changed = b, true
	}
	if b, deleted := stripAnthropicBodyFieldUnlessBeta(body, "thinking.block_binding", header, claude.BetaThinkingBindingControls); deleted {
		body, changed = b, true
	}
	if b, deleted := stripAnthropicBodyFieldUnlessBeta(body, "fallbacks", header, claude.BetaServerSideFallback); deleted {
		body, changed = b, true
	}
	if b, deleted := stripAnthropicBodyFieldUnlessBeta(body, "fallback_credit_token", header,
		claude.BetaServerSideFallback, claude.BetaFallbackCredit, claude.BetaFallbackCreditLegacy); deleted {
		body, changed = b, true
	}
	if b, deleted := stripAnthropicMessageOutputConfigUnlessBeta(body, header); deleted {
		body, changed = b, true
	}
	return body, changed
}

func legacySanitizeAnthropicFallbackFields(body []byte, header string) ([]byte, bool) {
	changed := false
	if updated, deleted := stripAnthropicBodyFieldUnlessBeta(body, "fallbacks", header, claude.BetaServerSideFallback); deleted {
		body, changed = updated, true
	}
	if updated, deleted := stripAnthropicBodyFieldUnlessBeta(body, "fallback_credit_token", header,
		claude.BetaServerSideFallback, claude.BetaFallbackCredit, claude.BetaFallbackCreditLegacy); deleted {
		body, changed = updated, true
	}
	return body, changed
}

// legacyBetaFieldsPresent answers, with the gjson lookups the sanitizers used, which guarded
// fields the body carries (the ground truth the probe is checked against).
func legacyBetaFieldsPresent(body []byte) betaFieldMask {
	var present betaFieldMask
	if gjson.GetBytes(body, "context_management").Exists() {
		present |= betaFieldContextManagement
	}
	if gjson.GetBytes(body, "thinking.block_binding").Exists() {
		present |= betaFieldThinkingBlockBinding
	}
	if gjson.GetBytes(body, "fallbacks").Exists() {
		present |= betaFieldFallbacks
	}
	if gjson.GetBytes(body, "fallback_credit_token").Exists() {
		present |= betaFieldFallbackCreditToken
	}
	if msgs := gjson.GetBytes(body, "messages"); msgs.Exists() && msgs.IsArray() {
		for _, msg := range msgs.Array() {
			if msg.Get("output_config").Exists() {
				present |= betaFieldMessageOutputConfig
				break
			}
		}
	}
	return present
}

var perf18BetaTokens = []string{
	anthropicBetaContextManagementToken, anthropicBetaCompactToken, claude.BetaThinkingBindingControls,
	claude.BetaServerSideFallback, claude.BetaFallbackCredit, claude.BetaFallbackCreditLegacy,
	claude.BetaMidConversationOutputConfig, "oauth-2025-04-20", "interleaved-thinking-2025-05-14", "claude-code-20250219",
}

func perf18RandBetaHeader(rng *rand.Rand) string {
	var parts []string
	for _, token := range perf18BetaTokens {
		if rng.Intn(3) == 0 {
			switch rng.Intn(6) {
			case 0:
				token = " " + token + "  "
			case 1:
				token = strings.ToUpper(token) // case matters: this one does not authorize anything
			case 2:
				token = token + "x"
			case 3:
				token = "　" + token // an ideographic space, trimmed by strings.TrimSpace
			}
			parts = append(parts, token)
		}
	}
	if rng.Intn(5) == 0 {
		parts = append(parts, "", " ")
	}
	rng.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
	return strings.Join(parts, ",")
}

func perf18RandMessage(rng *rand.Rand) string {
	switch rng.Intn(14) {
	case 0:
		return `"just a string"`
	case 1:
		return `42`
	case 2:
		return `null`
	case 3:
		return `[{"output_config":{"effort":"high"}}]` // an array element: has no members
	case 4:
		return `{"role":"system","content":[],"output_config":{"effort":"high"}}`
	case 5:
		return `{"output_config":null,"role":"user","content":"x"}`
	case 6: // output_config only deeper down: not a message member
		return `{"role":"user","content":[{"type":"text","text":"output_config","cache_control":{"type":"ephemeral"},"output_config":{"a":1}}],"meta":{"output_config":1}}`
	case 7: // escaped spelling of the key (§ stands for a backslash)
		return perf18Unesc(`{"role":"user","content":"x","§u006futput_config":{"effort":"low"}}`)
	case 8:
		return `{"role":"user","content":"output_config in the text only"}`
	case 9: // duplicate members
		return `{"role":"user","output_config":1,"content":"x","output_config":2}`
	case 10:
		return `{"role":"assistant","content":[{"type":"text","text":` + perf18RandString(rng) + `}]}`
	case 11:
		return `{ "role" : "user" , "content" : "x" , "Output_Config" : 1 }`
	default:
		return `{"role":"user","content":` + perf18RandString(rng) + `}`
	}
}

func perf18RandBetaBody(rng *rand.Rand) string {
	var members []string
	add := func(m string) { members = append(members, m) }
	pick := func(options ...string) string { return options[rng.Intn(len(options))] }

	add(`"model":"claude-opus-5-5"`)
	if rng.Intn(2) == 0 {
		add(`"max_tokens":4096`)
	}
	if rng.Intn(2) == 0 {
		add(`"output_config":{"effort":"high"}`) // top level: never counts as a message member
	}
	if rng.Intn(3) == 0 {
		add(`"system":` + pick(`"s"`, `[{"type":"text","text":"output_config"}]`, `null`))
	}
	if rng.Intn(3) == 0 {
		add(`"tools":[{"name":"t","input_schema":{"type":"object","properties":{"fallbacks":{"type":"string"}}}}]`)
	}
	for _, key := range []string{"context_management", "fallbacks", "fallback_credit_token"} {
		switch rng.Intn(8) {
		case 0:
			add(`"` + key + `":` + pick(`null`, `"default"`, `{"edits":[{"type":"clear_thinking_20251015"}]}`, `["a","b"]`, `1`))
		case 1: // twice
			add(`"` + key + `":"a"`)
			add(`"` + key + `":{"b":1}`)
		case 2: // an escaped spelling of the key
			add(fmt.Sprintf(`"\u%04x%s":"x"`, key[0], key[1:]))
		case 3: // wrong case or a neighbouring name: not the field
			add(pick(`"`+strings.ToUpper(key)+`":1`, `"`+key+`_x":1`, `"x_`+key+`":1`))
		}
	}
	switch rng.Intn(12) {
	case 0:
		add(`"thinking":{"type":"enabled","budget_tokens":2048}`)
	case 1:
		add(`"thinking":{"type":"adaptive","block_binding":"prefix"}`)
	case 2:
		add(`"thinking":{"adaptive":{"block_binding":"nested"}}`)
	case 3:
		add(`"thinking":"enabled"`)
	case 4:
		add(`"thinking":5`)
	case 5:
		add(`"thinking":[{"block_binding":"in an array"}]`)
	case 6: // duplicated: gjson keeps trying later duplicates
		add(`"thinking":"x"`)
		add(`"thinking":{"block_binding":"second"}`)
	case 7:
		add(perf18Unesc(`"thinking":{"§u0062lock_binding":"escaped"}`))
	case 8:
		add(`"thinking":null`)
	case 9:
		add(`"thinking":{"type":"enabled"}`)
		add(`"thinking":{"type":"enabled","block_binding":1}`)
	case 10:
		add(`"thinking":{ "block_binding" : true , "type" : "x" }`)
	}
	switch rng.Intn(10) {
	case 0: // absent
	case 1:
		add(`"messages":{"role":"user","output_config":{}}`) // not an array
	case 2:
		add(`"messages":"text"`)
	case 3: // first one decides, whatever it holds
		add(`"messages":{}`)
		add(`"messages":[{"role":"user","output_config":{}}]`)
	case 4:
		add(`"messages":[{"role":"user","content":"x"}]`)
		add(`"messages":[{"role":"user","output_config":{}}]`)
	case 5:
		add(`"messages":[{"role":"user","output_config":{}}]`)
	default:
		n := rng.Intn(6)
		msgs := make([]string, n)
		for i := range msgs {
			msgs[i] = perf18RandMessage(rng)
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
	switch rng.Intn(60) { // now and then not an object, or damaged
	case 0:
		return `[` + body + `]`
	case 1:
		return `"` + `fallbacks` + `"`
	case 2:
		return body[:len(body)/2]
	case 3:
		return ""
	}
	return body
}

// The probe is one-sided: a field it clears is a field the gjson lookup does not find. It also has to
// be useful, i.e. clear everything on the bodies that carry none of the fields.
func TestAnthropicBetaFieldsMaybeInIsOneSided(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	cleared, total := 0, 0
	for iter := 0; iter < 30000; iter++ {
		body := []byte(perf18RandBetaBody(rng))
		present := legacyBetaFieldsPresent(body)
		maybe := anthropicBetaFieldsMaybeIn(body, betaFieldsAll)
		if present&^maybe != 0 {
			t.Fatalf("probe cleared a field that is present: present=%05b maybe=%05b body=%q", present, maybe, body)
		}
		// restricting the question never adds an answer
		want := betaFieldMask(rng.Intn(32))
		if got := anthropicBetaFieldsMaybeIn(body, want); got != maybe&want {
			t.Fatalf("want=%05b: got=%05b, full answer %05b, body=%q", want, got, maybe, body)
		}
		total++
		if maybe&^present == 0 {
			cleared++ // exact answer
		}
	}
	// Not a correctness bound, a guard against a probe that gave up on everything: about half of
	// the random bodies here hold deliberately awkward shapes (duplicates, escaped keys, arrays where
	// objects belong) that are rightly answered "maybe"; the ordinary-body test below is the real
	// usefulness check.
	require.Greater(t, cleared*100/total, 40, "the probe must be exact on ordinary bodies")
}

func TestAnthropicBetaFieldsMaybeInOrdinaryBodiesClearEverything(t *testing.T) {
	for _, sz := range perf18Sizes {
		for _, shape := range []perf18Shape{perf18Prose, perf18Code} {
			body := perf18AnthropicBody(sz.size, shape, "")
			require.Equal(t, betaFieldMask(0), anthropicBetaFieldsMaybeIn(body, betaFieldsAll), "%s/%d", sz.name, shape)
		}
	}
	for field, bit := range map[string]betaFieldMask{
		`"context_management":{"edits":[]}`:              betaFieldContextManagement,
		`"fallbacks":"default"`:                          betaFieldFallbacks,
		`"fallback_credit_token":"tok"`:                  betaFieldFallbackCreditToken,
		`"thinking":{"block_binding":"x"}`:               betaFieldThinkingBlockBinding,
		`"thinking":{"type":"enabled"}`:                  0,
		`"metadata":{"fallbacks":"nested, not a field"}`: 0,
	} {
		body := perf18AnthropicBody(20_000, perf18Code, field)
		// a duplicate "thinking" member (the corpus already has one) is left to gjson
		want := bit
		if strings.HasPrefix(field, `"thinking"`) {
			want = betaFieldThinkingBlockBinding
		}
		require.Equal(t, want, anthropicBetaFieldsMaybeIn(body, betaFieldsAll), field)
	}
	// a message-level output_config in the middle of a big conversation
	body := perf18AnthropicBody(50_000, perf18Code, "")
	patched, err := sjson.SetRawBytes(body, "messages.1.output_config", []byte(`{"effort":"high"}`))
	require.NoError(t, err)
	require.Equal(t, betaFieldMessageOutputConfig, anthropicBetaFieldsMaybeIn(patched, betaFieldsAll))
}

func TestAnthropicBetaFieldsToStripMatchesTokenLookups(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for iter := 0; iter < 20000; iter++ {
		header := perf18RandBetaHeader(rng)
		has := func(tokens ...string) bool {
			for _, token := range tokens {
				if anthropicBetaTokensContains(header, token) {
					return true
				}
			}
			return false
		}
		var want betaFieldMask
		if !has(anthropicBetaContextManagementToken, anthropicBetaCompactToken) {
			want |= betaFieldContextManagement
		}
		if !has(claude.BetaThinkingBindingControls) {
			want |= betaFieldThinkingBlockBinding
		}
		if !has(claude.BetaServerSideFallback) {
			want |= betaFieldFallbacks
		}
		if !has(claude.BetaServerSideFallback, claude.BetaFallbackCredit, claude.BetaFallbackCreditLegacy) {
			want |= betaFieldFallbackCreditToken
		}
		if !has(claude.BetaMidConversationOutputConfig) {
			want |= betaFieldMessageOutputConfig
		}
		require.Equal(t, want, anthropicBetaFieldsToStrip(header), "header %q", header)
	}
	require.Equal(t, betaFieldsAll, anthropicBetaFieldsToStrip(""))
}

// The sanitizers return exactly what they returned before the probe, byte for byte and flag for flag,
// for every body shape (fields at the top level, nested, duplicated, escaped, in odd positions,
// messages that are not arrays, bodies that are not objects) and every beta header.
func TestSanitizeAnthropicBetaMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	changedBodies := 0
	for iter := 0; iter < 20000; iter++ {
		body := []byte(perf18RandBetaBody(rng))
		for k := 0; k < 4; k++ {
			header := perf18RandBetaHeader(rng)
			// full strip of both guarded groups too: no authorizing token at all
			if k == 0 {
				header = ""
			}
			wantOut, wantChanged := legacySanitizeAnthropicBodyForBetaTokens(append([]byte(nil), body...), header)
			gotOut, gotChanged := sanitizeAnthropicBodyForBetaTokens(append([]byte(nil), body...), header)
			if wantChanged != gotChanged || !bytes.Equal(wantOut, gotOut) {
				t.Fatalf("sanitizeAnthropicBodyForBetaTokens header=%q body=%q\n legacy=%q changed=%v\n new   =%q changed=%v",
					header, body, wantOut, wantChanged, gotOut, gotChanged)
			}
			if wantChanged {
				changedBodies++
			}
			// the fallback pass runs on the output of the first one, as at every call site
			wantOut2, wantChanged2 := legacySanitizeAnthropicFallbackFields(wantOut, header)
			gotOut2, gotChanged2 := sanitizeAnthropicFallbackFields(gotOut, header)
			if wantChanged2 != gotChanged2 || !bytes.Equal(wantOut2, gotOut2) {
				t.Fatalf("sanitizeAnthropicFallbackFields header=%q body=%q\n legacy=%q changed=%v\n new   =%q changed=%v",
					header, wantOut, wantOut2, wantChanged2, gotOut2, gotChanged2)
			}
			// and on the raw body
			wantOut3, wantChanged3 := legacySanitizeAnthropicFallbackFields(append([]byte(nil), body...), header)
			gotOut3, gotChanged3 := sanitizeAnthropicFallbackFields(append([]byte(nil), body...), header)
			if wantChanged3 != gotChanged3 || !bytes.Equal(wantOut3, gotOut3) {
				t.Fatalf("sanitizeAnthropicFallbackFields(raw) header=%q body=%q\n legacy=%q changed=%v\n new   =%q changed=%v",
					header, body, wantOut3, wantChanged3, gotOut3, gotChanged3)
			}
		}
	}
	require.Greater(t, changedBodies, 2000, "the corpus has to exercise the strip paths, not only the no-op")
	for _, body := range []string{``, `{}`, `[]`, `null`, `{"fallbacks":1,"fallbacks":2,"fallbacks":3}`, `{"fallbacks":1,"fallbacks":2}`} {
		wantOut, wantChanged := legacySanitizeAnthropicBodyForBetaTokens([]byte(body), "")
		gotOut, gotChanged := sanitizeAnthropicBodyForBetaTokens([]byte(body), "")
		require.Equal(t, wantChanged, gotChanged, body)
		require.Equal(t, string(wantOut), string(gotOut), body)
		wantOut, wantChanged = legacySanitizeAnthropicFallbackFields(wantOut, "")
		gotOut, gotChanged = sanitizeAnthropicFallbackFields(gotOut, "")
		require.Equal(t, wantChanged, gotChanged, body)
		require.Equal(t, string(wantOut), string(gotOut), body)
	}
}

// The point of the probe: the common request (none of the guarded fields) costs no allocation and no
// per-field lookup. The legacy lookups copy the messages array, which is what the control shows.
func TestSanitizeAnthropicBetaOrdinaryBodyAllocatesNothing(t *testing.T) {
	body := perf18AnthropicBody(120_000, perf18Code, "")
	header := "oauth-2025-04-20,interleaved-thinking-2025-05-14"

	legacy := testing.AllocsPerRun(10, func() {
		out, _ := legacySanitizeAnthropicBodyForBetaTokens(body, header)
		legacySanitizeAnthropicFallbackFields(out, header)
	})
	require.Greater(t, legacy, 0.0, "control: the legacy path copies the messages array")

	fast := testing.AllocsPerRun(10, func() {
		out, changed := sanitizeAnthropicBodyForBetaTokens(body, header)
		if changed {
			t.Fatal("unexpected change")
		}
		if _, changed := sanitizeAnthropicFallbackFields(out, header); changed {
			t.Fatal("unexpected change")
		}
	})
	require.Zero(t, fast)

	// the strip paths still work on a big body
	withFields := perf18AnthropicBody(120_000, perf18Code, `"context_management":{"edits":[]},"fallbacks":"default"`)
	out, changed := sanitizeAnthropicBodyForBetaTokens(withFields, header)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "context_management").Exists())
	require.False(t, gjson.GetBytes(out, "fallbacks").Exists())
	kept, changed := sanitizeAnthropicBodyForBetaTokens(withFields, header+","+claude.BetaServerSideFallback+","+anthropicBetaContextManagementToken)
	require.False(t, changed)
	require.True(t, bytes.Equal(withFields, kept))
}
