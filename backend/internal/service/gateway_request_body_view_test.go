//go:build unit

package service

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// refReplaceBody and refCloneForBody are ReplaceBody / CloneForBody exactly as they were
// before the body view: every call validates and scans the whole body.
func refReplaceBody(p *ParsedRequest, data []byte) error {
	if p.Body == nil {
		p.Body = NewRequestBodyRef(data)
	} else {
		p.Body.Replace(data)
	}
	if err := parseGatewayRequestCurrentBody(p, p.protocol); err != nil {
		clearGatewayRequestRanges(p)
		return err
	}
	return nil
}

func refCloneForBody(p *ParsedRequest, body []byte) (*ParsedRequest, error) {
	clone := *p
	clone.Body = NewRequestBodyRef(body)
	clone.OnUpstreamAccepted = nil
	if err := parseGatewayRequestCurrentBody(&clone, clone.protocol); err != nil {
		return nil, err
	}
	return &clone, nil
}

func requireSameParsedState(t *testing.T, want, got *ParsedRequest, ctx string) {
	t.Helper()
	require.Equal(t, want.Model, got.Model, "%s: Model", ctx)
	require.Equal(t, want.Stream, got.Stream, "%s: Stream", ctx)
	require.Equal(t, want.MetadataUserID, got.MetadataUserID, "%s: MetadataUserID", ctx)
	require.Equal(t, want.HasSystem, got.HasSystem, "%s: HasSystem", ctx)
	require.Equal(t, want.ThinkingEnabled, got.ThinkingEnabled, "%s: ThinkingEnabled", ctx)
	require.Equal(t, want.OutputEffort, got.OutputEffort, "%s: OutputEffort", ctx)
	require.Equal(t, want.Speed, got.Speed, "%s: Speed", ctx)
	require.Equal(t, want.MaxTokens, got.MaxTokens, "%s: MaxTokens", ctx)
	require.Equal(t, want.protocol, got.protocol, "%s: protocol", ctx)
	require.Equal(t, want.systemRange, got.systemRange, "%s: systemRange", ctx)
	require.Equal(t, want.messagesRange, got.messagesRange, "%s: messagesRange", ctx)
	require.Equal(t, want.inputRange, got.inputRange, "%s: inputRange", ctx)
	require.Equal(t, want.OnUpstreamAccepted == nil, got.OnUpstreamAccepted == nil, "%s: OnUpstreamAccepted", ctx)
	require.Equal(t, want.SessionContext, got.SessionContext, "%s: SessionContext", ctx)
	require.Equal(t, want.GroupID, got.GroupID, "%s: GroupID", ctx)
	require.True(t, bytes.Equal(want.Body.Bytes(), got.Body.Bytes()), "%s: Body bytes differ", ctx)
	for name, pair := range map[string][2][]byte{
		"SystemRaw":   {want.SystemRaw(), got.SystemRaw()},
		"MessagesRaw": {want.MessagesRaw(), got.MessagesRaw()},
		"InputRaw":    {want.InputRaw(), got.InputRaw()},
	} {
		require.Equal(t, pair[0] == nil, pair[1] == nil, "%s: %s nil-ness", ctx, name)
		require.True(t, bytes.Equal(pair[0], pair[1]), "%s: %s", ctx, name)
	}
}

func requireSameErr(t *testing.T, want, got error, ctx string) {
	t.Helper()
	require.Equal(t, want == nil, got == nil, "%s: want err %v, got %v", ctx, want, got)
	if want != nil {
		require.Equal(t, want.Error(), got.Error(), "%s: error text", ctx)
	}
}

// perf18ViewCorpus is the bodies the view tests start from: every shape that changes
// which fields and ranges exist.
func perf18ViewCorpus() map[string]string {
	return map[string]string{
		"anthropic-full":        `{"model":"claude-sonnet-4-5","stream":true,"max_tokens":4096,"metadata":{"user_id":"u1"},"thinking":{"type":"enabled"},"system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":" high "},"speed":"Fast"}`,
		"anthropic-1m":          `{"model":"claude-sonnet-4-5[1m][1M]","stream":false,"messages":[{"role":"user","content":"hi"}]}`,
		"responses":             `{"model":"gpt-5","input":[{"role":"user","content":"x"}],"stream":true,"max_tokens":12}`,
		"responses-string":      `{"model":"gpt-5","input":"hello"}`,
		"gemini":                `{"model":"gemini-2.5-pro","contents":[{"role":"user","parts":[{"text":"x"}]}],"systemInstruction":{"parts":[{"text":"sys"}]}}`,
		"system-null":           `{"model":"m","system":null,"messages":[]}`,
		"escapes":               `{"model":"claude-x\"\\\\","stream":false,"messages":[{"content":"a\\\"}]b"}],"system":"` + "\xe2\x81\xa3" + `😀"}`,
		"whitespace":            "{\n  \"model\" : \"m\" ,\n \"stream\" : true ,\n \"messages\" : [ ]\n}",
		"no-fields":             `{"messages":[]}`,
		"empty-object":          `{}`,
		"top-level-array":       `[1,2,3]`,
		"top-level-null":        `null`,
		"max-tokens-float":      `{"model":"m","max_tokens":4096.7}`,
		"generated-code-shape":  string(perf18AnthropicBody(6000, perf18Code, "")),
		"generated-prose-shape": string(perf18AnthropicBody(6000, perf18Prose, `"fallbacks":"default"`)),
	}
}

func perf18ViewProtocols() []string {
	return []string{domain.PlatformAnthropic, "", domain.PlatformGemini, "responses"}
}

// TestParsedRequestViewReuseMatchesFullParse drives a ParsedRequest with the view and a
// reference that always re-parses through the same random sequence of ReplaceBody /
// CloneForBody calls (same slice, equal copy, sjson rewrites, invalid bodies, bodies from
// earlier in the sequence, callers editing the derived fields in between, direct writes to
// Body) and requires identical fields, ranges, bodies and errors after every step.
func TestParsedRequestViewReuseMatchesFullParse(t *testing.T) {
	models := []string{"claude-opus-4-5", "claude-sonnet-4-5[1m]", "gpt-5.4", `we"ird\model`, "café", ""}
	for name, base := range perf18ViewCorpus() {
		for _, protocol := range perf18ViewProtocols() {
			for seed := int64(0); seed < 12; seed++ {
				name, base, protocol, seed := name, base, protocol, seed
				t.Run(fmt.Sprintf("%s/%q/%d", name, protocol, seed), func(t *testing.T) {
					rng := rand.New(rand.NewSource(seed*7919 + int64(len(name))))
					got, gotErr := ParseGatewayRequest(NewRequestBodyRef([]byte(base)), protocol)
					want := &ParsedRequest{Body: NewRequestBodyRef([]byte(base))}
					wantErr := parseGatewayRequestCurrentBody(want, protocol)
					requireSameErr(t, wantErr, gotErr, "initial parse")
					if gotErr != nil {
						return
					}
					history := [][]byte{[]byte(base)}
					for step := 0; step < 40; step++ {
						ctx := fmt.Sprintf("step %d", step)
						cur := append([]byte(nil), got.Body.Bytes()...)
						var next []byte
						op := rng.Intn(13)
						switch op {
						case 0: // same slice: the no-op rewrite
							ctx += " same-slice"
							requireSameErr(t, refReplaceBody(want, want.Body.Bytes()), got.ReplaceBody(got.Body.Bytes()), ctx)
						case 1: // an equal copy
							ctx += " equal-copy"
							requireSameErr(t, refReplaceBody(want, append([]byte(nil), cur...)), got.ReplaceBody(append([]byte(nil), cur...)), ctx)
						case 2: // model rewrite (what the account / channel model mapping does)
							ctx += " model"
							next = ReplaceModelInBody(cur, models[rng.Intn(len(models))])
						case 3:
							ctx += " stream"
							next, _ = sjson.SetBytes(cur, "stream", rng.Intn(2) == 0)
						case 4:
							ctx += " max_tokens"
							next, _ = sjson.SetBytes(cur, "max_tokens", rng.Intn(100000))
						case 5:
							ctx += " drop-system"
							next, _ = sjson.DeleteBytes(cur, "system")
						case 6:
							ctx += " messages"
							next, _ = sjson.SetRawBytes(cur, "messages", []byte(fmt.Sprintf(`[{"role":"user","content":"%d"}]`, rng.Intn(1000))))
						case 7: // possibly invalid: flip one byte
							ctx += " byte-flip"
							next = append([]byte(nil), cur...)
							if len(next) > 0 {
								next[rng.Intn(len(next))] = "\"\\{}[],:a1 \n"[rng.Intn(12)]
							}
						case 8: // invalid for sure
							ctx += " truncated"
							next = cur[:len(cur)/2]
						case 9: // an earlier body again: the view holds only the latest one
							ctx += " history"
							next = append([]byte(nil), history[rng.Intn(len(history))]...)
						case 10: // callers edit the derived fields, then rewrite with the same bytes
							ctx += " edited-fields-then-same"
							for _, p := range []*ParsedRequest{want, got} {
								p.Model, p.Stream, p.MaxTokens, p.MetadataUserID = "edited", !p.Stream, 7, "edited"
								p.HasSystem, p.ThinkingEnabled, p.OutputEffort, p.Speed = !p.HasSystem, !p.ThinkingEnabled, "edited", "edited"
								p.systemRange, p.messagesRange, p.inputRange = jsonRange{start: 1, end: 2}, jsonRange{start: 3, end: 4}, missingJSONRange()
							}
							requireSameErr(t, refReplaceBody(want, want.Body.Bytes()), got.ReplaceBody(got.Body.Bytes()), ctx)
						case 11: // direct write to Body (bypasses ReplaceBody), refreshed by the next call
							ctx += " direct-body-write"
							other := append([]byte(nil), history[rng.Intn(len(history))]...)
							want.Body.Replace(append([]byte(nil), other...))
							got.Body.Replace(append([]byte(nil), other...))
							requireSameErr(t, refReplaceBody(want, want.Body.Bytes()), got.ReplaceBody(got.Body.Bytes()), ctx)
						case 12: // failover clone with the same / changed / invalid body
							ctx += " clone"
							var body []byte
							switch rng.Intn(3) {
							case 0:
								body = cur
							case 1:
								body = ReplaceModelInBody(cur, models[rng.Intn(len(models))])
							default:
								body = cur[:len(cur)/3]
							}
							got.OnUpstreamAccepted = func() {}
							want.OnUpstreamAccepted = func() {}
							wantClone, wantCloneErr := refCloneForBody(want, append([]byte(nil), body...))
							gotClone, gotCloneErr := got.CloneForBody(append([]byte(nil), body...))
							requireSameErr(t, wantCloneErr, gotCloneErr, ctx)
							got.OnUpstreamAccepted, want.OnUpstreamAccepted = nil, nil
							if gotCloneErr == nil {
								requireSameParsedState(t, wantClone, gotClone, ctx)
								// carry on with the clone, as a failover attempt does
								got, want = gotClone, wantClone
							}
						}
						if next != nil {
							requireSameErr(t, refReplaceBody(want, append([]byte(nil), next...)), got.ReplaceBody(append([]byte(nil), next...)), ctx)
							history = append(history, next)
						}
						requireSameParsedState(t, want, got, ctx)
					}
				})
			}
		}
	}
}

// A rewrite that returns the very same slice (StripEmptyTextBlocks and friends) must not
// validate or scan again: the full parse allocates, the reuse path does not.
func TestParsedRequestReplaceBodyReusesViewWithoutParsing(t *testing.T) {
	body := perf18AnthropicBody(20_000, perf18Code, "")
	p, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	require.NotZero(t, p.MaxTokens)

	// Control: the full parse allocates, so a zero below can only come from skipping it.
	fullParse := testing.AllocsPerRun(20, func() {
		if err := refReplaceBody(p, p.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	})
	require.Greater(t, fullParse, 0.0)
	fullClone := testing.AllocsPerRun(20, func() {
		if _, err := refCloneForBody(p, p.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	})

	require.NoError(t, p.ReplaceBody(p.Body.Bytes())) // establishes the view for the cases below
	require.Zero(t, testing.AllocsPerRun(20, func() {
		if err := p.ReplaceBody(p.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	}), "same slice: the parsed view is reused")

	a, b := p.Body.Bytes(), append([]byte(nil), p.Body.Bytes()...)
	require.Zero(t, testing.AllocsPerRun(20, func() {
		a, b = b, a
		if err := p.ReplaceBody(a); err != nil {
			t.Fatal(err)
		}
	}), "equal content in another slice: the parsed view is reused")

	clone := testing.AllocsPerRun(20, func() {
		if _, err := p.CloneForBody(p.Body.Bytes()); err != nil {
			t.Fatal(err)
		}
	})
	require.LessOrEqual(t, clone, 2.0, "a clone costs its own ParsedRequest and body reference, no parse")
	require.Less(t, clone, fullClone)
}

// The view follows the current slice: after an equal copy replaced the body the old array
// may be reused or overwritten by its owner, and the view must not be anchored to it.
func TestParsedRequestViewFollowsCurrentSlice(t *testing.T) {
	body := perf18AnthropicBody(8_000, perf18Code, "")
	p, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	fresh := append([]byte(nil), body...)
	require.NoError(t, p.ReplaceBody(fresh))
	require.Same(t, &fresh[0], &p.view.body[0], "view must point at the slice that is now Body")
	for i := range body { // the owner of the old array scribbles over it (a test-only violation of the no-in-place rule)
		body[i] = 'x'
	}
	require.Zero(t, testing.AllocsPerRun(20, func() { require.NoError(t, p.ReplaceBody(fresh)) }))
}

// Bodies that differ from the parsed one in any way are parsed in full, validity gate included.
func TestParsedRequestReplaceBodyChangedContentIsParsed(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","stream":false,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`)
	p, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)

	t.Run("same length, one byte different", func(t *testing.T) {
		next := bytes.Replace(body, []byte(`"stream":false`), []byte(`"stream":true `), 1)
		require.Equal(t, len(body), len(next))
		require.NoError(t, p.ReplaceBody(next))
		require.True(t, p.Stream)
	})
	t.Run("difference in the last byte", func(t *testing.T) {
		cur := append([]byte(nil), p.Body.Bytes()...)
		bad := append([]byte(nil), cur...)
		bad[len(bad)-1] = ' ' // drops the closing brace, same length
		require.Error(t, p.ReplaceBody(bad))
		require.Error(t, p.ReplaceBody(bad), "the same invalid body fails again, it does not inherit a view")
		require.False(t, p.HasSystem)
		require.Nil(t, p.MessagesRaw(), "ranges are cleared by a failed replace")
		require.NoError(t, p.ReplaceBody(cur), "and the valid body recovers")
		require.NotNil(t, p.MessagesRaw())
	})
	t.Run("invalid body never takes the reuse path", func(t *testing.T) {
		require.Error(t, p.ReplaceBody([]byte(`{"model":`)))
		require.Empty(t, p.view.body, "a failed parse leaves no view behind")
	})
	t.Run("duplicate model key is still rejected", func(t *testing.T) {
		require.ErrorIs(t, p.ReplaceBody([]byte(`{"model":"a","MODEL":"b"}`)), ErrDuplicateModelField)
	})
	t.Run("direct write to Body is not mistaken for a parsed body", func(t *testing.T) {
		good := []byte(`{"model":"m1","messages":[]}`)
		require.NoError(t, p.ReplaceBody(good))
		p.Body.Replace([]byte(`{"model":"m2","messages":[]}`))
		require.NoError(t, p.ReplaceBody(p.Body.Bytes()))
		require.Equal(t, "m2", p.Model)
	})
}

// The derived fields come back exactly as a re-parse yields them even after callers edited them.
func TestParsedRequestReplaceBodyRestoresEditedFields(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-5","stream":true,"max_tokens":321,"metadata":{"user_id":"u9"},"thinking":{"type":"adaptive"},"system":"s","messages":[],"output_config":{"effort":"max"},"speed":"fast"}`)
	p, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	want := *p

	p.Model, p.Stream, p.MaxTokens, p.MetadataUserID = "mapped", false, 1, "other"
	p.HasSystem, p.ThinkingEnabled, p.OutputEffort, p.Speed = false, false, "low", ""
	p.systemRange, p.messagesRange, p.inputRange = missingJSONRange(), missingJSONRange(), jsonRange{start: 5, end: 9}
	require.NoError(t, p.ReplaceBody(p.Body.Bytes()))

	requireSameParsedState(t, &want, p, "restored")
}

// A view built for one protocol is not used for another (the ranges differ).
func TestParsedRequestViewIsPerProtocol(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"role":"user","content":"x"}],"messages":[]}`)
	p, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	require.Nil(t, p.InputRaw(), "anthropic has no input range")

	require.NoError(t, refreshGatewayRequestRanges(p, "responses"))
	require.NotNil(t, p.InputRaw(), "responses reads the input range from the same bytes")
	require.Equal(t, "responses", p.protocol)
}

// The first parse of a body is the client-facing validity gate: it never consults a view.
func TestParseGatewayRequestAlwaysValidates(t *testing.T) {
	bad := []byte(`{"model":"m","messages":[1,2,}`)
	require.False(t, gjson.ValidBytes(bad))
	ref := NewRequestBodyRef(bad)
	_, err := ParseGatewayRequest(ref, domain.PlatformAnthropic)
	require.Error(t, err)

	p := &ParsedRequest{Body: NewRequestBodyRef([]byte(`{"model":"m"}`))}
	require.NoError(t, parseGatewayRequestCurrentBody(p, ""))
	p.Body.Replace(bad)
	require.Error(t, parseGatewayRequestCurrentBody(p, ""), "parseGatewayRequestCurrentBody has no reuse path")
}

// CloneForBody gives each failover attempt an independent view: rewriting one attempt's body
// neither changes the parent nor the next attempt.
func TestParsedRequestCloneForBodyIsIndependent(t *testing.T) {
	body := perf18AnthropicBody(8_000, perf18Code, "")
	parent, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	parent.OnUpstreamAccepted = func() {}

	first, err := parent.CloneForBody(body)
	require.NoError(t, err)
	require.Nil(t, first.OnUpstreamAccepted, "OnUpstreamAccepted is per attempt")
	require.NotNil(t, parent.OnUpstreamAccepted)
	require.NotSame(t, parent.Body, first.Body)

	require.NoError(t, first.ReplaceBody(ReplaceModelInBody(first.Body.Bytes(), "mapped-model")))
	require.Equal(t, "mapped-model", first.Model)
	require.Contains(t, string(first.Body.Bytes()), `"model":"mapped-model"`)
	require.Equal(t, "claude-sonnet-4-5", parent.Model)
	require.True(t, bytes.Equal(body, parent.Body.Bytes()))

	second, err := parent.CloneForBody(body)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", second.Model)
	require.True(t, bytes.Equal(body, second.Body.Bytes()))

	_, err = parent.CloneForBody([]byte(`{"model":`))
	require.Error(t, err)
	var nilParent *ParsedRequest
	_, err = nilParent.CloneForBody(body)
	require.Error(t, err)
}

// [1m] is stripped from the model by the first parse; the stripped body is what the view
// describes, and replacing the body with the original (unstripped) bytes parses again.
func TestParsedRequestViewAfterLongContextSuffixNormalization(t *testing.T) {
	original := []byte(`{"model":"claude-sonnet-4-5[1m]","messages":[]}`)
	p, err := ParseGatewayRequest(NewRequestBodyRef(append([]byte(nil), original...)), domain.PlatformAnthropic)
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", p.Model)
	normalized := append([]byte(nil), p.Body.Bytes()...)
	require.NotEqual(t, original, normalized)

	require.NoError(t, p.ReplaceBody(append([]byte(nil), original...)))
	require.Equal(t, "claude-sonnet-4-5", p.Model)
	require.Equal(t, normalized, p.Body.Bytes(), "the suffix is stripped again, as on the first parse")

	require.NoError(t, p.ReplaceBody(normalized))
	require.Equal(t, "claude-sonnet-4-5", p.Model)
}
