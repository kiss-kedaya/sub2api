package service

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyHasEmptyTextMember is the pre-filter StripEmptyTextBlocks and FilterThinkingBlocksForRetry used
// before containsEmptyTextMember: four independent substring searches.
func legacyHasEmptyTextMember(body []byte) bool {
	return bytes.Contains(body, patternEmptyText) ||
		bytes.Contains(body, patternEmptyTextSpaced) ||
		bytes.Contains(body, patternEmptyTextSp1) ||
		bytes.Contains(body, patternEmptyTextSp2)
}

// Every spelling around the key, alone and inside text, plus near misses of each kind: other
// whitespace, other case, a longer key that ends in "text", a non-empty value, a value cut short.
func TestContainsEmptyTextMemberMatchesFourSearches(t *testing.T) {
	keys := []string{`"text"`, `"Text"`, `"texts"`, `"subtext"`, `text`, `"tex"`, `"te xt"`, `"text`, `text"`, `""text""`, `"\"text\""`}
	beforeColon := []string{"", " ", "  ", "\t", "\n"}
	afterColon := []string{"", " ", "  ", "\t", "\n"}
	values := []string{`""`, `"`, `" "`, `"a"`, `"` + `"` + `"`, ``, `"\"`, `null`, `''`}
	for _, key := range keys {
		for _, b := range beforeColon {
			for _, a := range afterColon {
				for _, v := range values {
					member := key + b + ":" + a + v
					for _, body := range []string{
						member,
						`{"type":"text",` + member + `}`,
						`xxxxxxxxxxxxxxxxxxxx` + member,
						member + `xxxxxxxxxxxxxxxxxxxx`,
						`x` + member,
						member[:len(member)-1],
					} {
						require.Equal(t, legacyHasEmptyTextMember([]byte(body)), containsEmptyTextMember([]byte(body)), "body %q", body)
					}
				}
			}
		}
	}
}

func TestContainsEmptyTextMemberRandomBodies(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	pieces := []string{
		`"text":""`, `"text": ""`, `"text" : ""`, `"text" :""`, `"text":"a"`, `"text"`, `"text":`, `"text":"`, `"type":"text"`,
		`"content":[]`, `{`, `}`, `,`, ` `, `x`, `xx`, `"x"`, `text`, `"`, `""`, `:`, `"messages":[`, `]`, `é`, `\n`, `\"text\":\"\"`,
		`"role":"user"`, `AAAAxAAAA`, `"te`, `xt":""`,
	}
	hit, miss := 0, 0
	for iter := 0; iter < 80000; iter++ {
		var sb strings.Builder
		for i, n := 0, rng.Intn(16); i < n; i++ {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		body := []byte(sb.String())
		want := legacyHasEmptyTextMember(body)
		require.Equal(t, want, containsEmptyTextMember(body), "body %q", body)
		if want {
			hit++
		} else {
			miss++
		}
	}
	require.Greater(t, hit, 2000)
	require.Greater(t, miss, 2000)
	for _, body := range [][]byte{nil, {}, []byte("x"), []byte(`"text"`), []byte(`"text":""`), []byte(`"text":"`)} {
		require.Equal(t, legacyHasEmptyTextMember(body), containsEmptyTextMember(body), "body %q", body)
	}
	// a long body whose only empty text member is the very last thing in it, and one that has none
	big := perf18AnthropicBody(300_000, perf18Code, "")
	require.False(t, containsEmptyTextMember(big))
	require.Equal(t, legacyHasEmptyTextMember(big), containsEmptyTextMember(big))
	withTail := append(append([]byte(nil), big[:len(big)-2]...), []byte(`,{"role":"user","content":[{"type":"text","text":""}]}]}`)...)
	require.True(t, containsEmptyTextMember(withTail))
	require.True(t, legacyHasEmptyTextMember(withTail))
}

// StripEmptyTextBlocks itself: bodies without an empty text block come back as the very same slice, bodies with
// one lose it.
func TestStripEmptyTextBlocksUsesTheProbe(t *testing.T) {
	big := perf18AnthropicBody(60_000, perf18Code, "")
	out := StripEmptyTextBlocks(big)
	require.Same(t, &big[0], &out[0], "no empty text block: the same slice")

	withEmpty := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"keep"}]}]}`)
	stripped := StripEmptyTextBlocks(withEmpty)
	require.NotContains(t, string(stripped), `"text":""`)
	require.Contains(t, string(stripped), `"keep"`)
	for _, spelled := range []string{`"text": ""`, `"text" : ""`, `"text" :""`} {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text",` + spelled + `},{"type":"text","text":"keep"}]}]}`)
		stripped := StripEmptyTextBlocks(body)
		require.NotEqual(t, string(body), string(stripped), spelled)
		require.Contains(t, string(stripped), `keep`)
	}
}
