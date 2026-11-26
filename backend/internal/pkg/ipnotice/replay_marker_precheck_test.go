package ipnotice

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyHasReplayMarker is the precheck NormalizeReplay used before hasReplayMarker: three
// independent searches, any hit lets the body through to the full pass.
func legacyHasReplayMarker(body []byte) bool {
	return bytes.Contains(body, []byte(noticeFence)) ||
		bytes.Contains(body, []byte(strings.ReplaceAll("§u2063§u2063", "§", `\`))) ||
		bytes.Contains(body, []byte("msg_gateway_notice_"))
}

// Fragments that sit next to the three markers, so the random bodies hit every near miss: the id
// tail without its head, the id without the trailing underscore, one half of a fence, one escape.
var replayMarkerFragments = []string{
	noticeFence, noticeFence[:3], noticeFence[3:], "⁣", "⁢⁣",
	strings.ReplaceAll("§u2063§u2063", "§", `\`), strings.ReplaceAll("§u2063", "§", `\`), strings.ReplaceAll("§u2063§u2062", "§", `\`),
	strings.ReplaceAll("§§u2063§u2063", "§", `\`), "u2063u2063", "2063",
	"msg_gateway_notice_", "msg_gateway_notice", "_gateway_notice_", "gateway_notice_", "xmsg_gateway_notice_1",
	"msg_gateway_notices", "msg_gateway_notic_", "msg_Gateway_notice_", "msg-gateway-notice-",
	"msg_", "_notice_", "gateway", "notice",
}

func TestHasReplayMarkerMatchesThreeSearches(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	filler := []string{"a", "bc", "the value", "\\n", `\"`, "é", "—", "😀", "_", "m", "msg", " ", "{\"a\":1}", "AAAA", "9"}
	hit, miss := 0, 0
	for iter := 0; iter < 60000; iter++ {
		var sb strings.Builder
		for i, n := 0, rng.Intn(14); i < n; i++ {
			if rng.Intn(4) == 0 {
				sb.WriteString(replayMarkerFragments[rng.Intn(len(replayMarkerFragments))])
			} else {
				sb.WriteString(filler[rng.Intn(len(filler))])
			}
		}
		body := []byte(sb.String())
		want := legacyHasReplayMarker(body)
		require.Equal(t, want, hasReplayMarker(body), "body %q", body)
		if want {
			hit++
		} else {
			miss++
		}
	}
	require.Greater(t, hit, 3000)
	require.Greater(t, miss, 3000)
	for _, body := range [][]byte{nil, {}, []byte("_gateway_notice_"), []byte("msg_gateway_notice_"), []byte(noticeFence)} {
		require.Equal(t, legacyHasReplayMarker(body), hasReplayMarker(body), "body %q", body)
	}
}

// Bodies without any marker go back untouched, as the very same slice.
func TestNormalizeReplayPrecheckKeepsBodiesWithoutMarkersUntouched(t *testing.T) {
	// no marker: the very same slice comes back, whatever the protocol and however odd the JSON
	for _, protocol := range []Protocol{Anthropic, Chat, Responses, Gemini, ""} {
		for _, body := range []string{
			`{"messages":[{"role":"assistant","content":[{"type":"text","text":"plain"}]}]}`,
			`{"input":[{"type":"item_reference","id":"msg_gateway"}]}`,
			`{"messages":[{"role":"assistant","content":"_gateway_notice_ is only a tail"}]}`,
			`not json at all`, ``,
		} {
			in := []byte(body)
			out := NormalizeReplay(in, protocol)
			require.Equal(t, len(in), len(out))
			if len(in) > 0 {
				require.Same(t, &in[0], &out[0], "protocol %q body %q must come back as the same slice", protocol, body)
			}
		}
	}
	// a body that does carry an id is processed (a Responses item reference with the id is dropped)
	body := []byte(`{"input":[{"type":"item_reference","id":"msg_gateway_notice_1"},{"role":"user","content":"hi"}]}`)
	out := NormalizeReplay(body, Responses)
	require.NotContains(t, string(out), "msg_gateway_notice_")
	require.Contains(t, string(out), `"content":"hi"`)
}
