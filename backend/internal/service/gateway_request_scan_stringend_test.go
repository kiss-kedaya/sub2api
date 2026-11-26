//go:build unit

package service

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

// legacyScanJSONStringEnd is scanJSONStringEnd as it was before the IndexByte version: look at
// every byte, a backslash swallows the byte after it, the first quote that is left ends the string.
func legacyScanJSONStringEnd(body []byte, i int) (int, error) {
	for j := i + 1; j < len(body); j++ {
		switch body[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string at byte %d", i)
}

// legacyScanJSONCompositeEnd is scanJSONCompositeEnd on top of the legacy string scan.
func legacyScanJSONCompositeEnd(body []byte, i int) (int, error) {
	depth := 0
	for j := i; j < len(body); j++ {
		switch body[j] {
		case '"':
			end, err := legacyScanJSONStringEnd(body, j)
			if err != nil {
				return 0, err
			}
			j = end - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return j + 1, nil
			}
			if depth < 0 {
				return 0, fmt.Errorf("unbalanced bracket at byte %d", j)
			}
		}
	}
	return 0, fmt.Errorf("unterminated composite value at byte %d", i)
}

func requireSameStringEnd(t *testing.T, body []byte, i int) {
	t.Helper()
	wantEnd, wantErr := legacyScanJSONStringEnd(body, i)
	gotEnd, gotErr := scanJSONStringEnd(body, i)
	if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) || wantEnd != gotEnd {
		t.Fatalf("scanJSONStringEnd(%q, %d): legacy=(%d, %v) new=(%d, %v)", body, i, wantEnd, wantErr, gotEnd, gotErr)
	}
}

// Every string of up to eight bytes over an alphabet that has the quote, the backslash and
// ordinary bytes (including the ones that follow a backslash in real escapes), alone and
// behind a prefix whose backslashes must not count towards the string.
func TestScanJSONStringEndExhaustiveMatchesLegacy(t *testing.T) {
	alphabet := []byte{'"', '\\', 'a', 'n', 'u'}
	const maxLen = 8
	prefixes := [][]byte{nil, []byte(`\`), []byte(`\\\`), []byte(`{"k":`), []byte(`a\\`)}
	buf := make([]byte, 0, maxLen+8)
	var walk func(content []byte)
	walk = func(content []byte) {
		for _, prefix := range prefixes {
			buf = append(buf[:0], prefix...)
			buf = append(buf, '"')
			buf = append(buf, content...)
			requireSameStringEnd(t, buf, len(prefix))
		}
		if len(content) == maxLen {
			return
		}
		for _, c := range alphabet {
			walk(append(content, c))
		}
	}
	walk(make([]byte, 0, maxLen))
}

func TestScanJSONStringEndRunsOfBackslashes(t *testing.T) {
	for run := 0; run <= 2100; run++ {
		for _, tail := range []string{`"`, `"x`, `"x"`, `""`, `\`, `n"`, ``} {
			requireSameStringEnd(t, []byte(`"ab`+strings.Repeat(`\`, run)+tail), 0)
		}
		// the same run in the middle of a longer string, with a real end after it
		requireSameStringEnd(t, []byte(`"`+strings.Repeat("é", 40)+strings.Repeat(`\`, run)+`"tail"more`), 0)
	}
}

func TestScanJSONStringEndBufferEdges(t *testing.T) {
	for _, s := range []string{`"`, `""`, `"\`, `"\"`, `"\\"`, `"\\\"`, `"a`, `"a"`, `"a\`, "\"\x00\"", "\"\n\"", `"é"`, `"😀"`, `"😀"`} {
		requireSameStringEnd(t, []byte(s), 0)
		requireSameStringEnd(t, []byte("xx"+s), 2)
		requireSameStringEnd(t, []byte(s+s), 0)
	}
}

// Random strings with escape and quote densities from none to every other byte, long enough
// to cross the SIMD block sizes of IndexByte, plus random cut points for the unterminated case.
func TestScanJSONStringEndRandomMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	for iter := 0; iter < 20000; iter++ {
		n := rng.Intn(300)
		if iter%97 == 0 {
			n = 4000 + rng.Intn(4000)
		}
		escapeEvery := 1 + rng.Intn(60)
		quoteEvery := 1 + rng.Intn(200)
		var b bytes.Buffer
		b.WriteByte('"')
		for b.Len() < n {
			switch {
			case rng.Intn(escapeEvery) == 0:
				b.WriteByte('\\')
				b.WriteByte("\"\\/bfnrtu0é"[rng.Intn(10)])
			case rng.Intn(quoteEvery) == 0:
				b.WriteByte('"')
			default:
				b.WriteByte(byte('a' + rng.Intn(26)))
			}
		}
		raw := b.Bytes()
		requireSameStringEnd(t, raw, 0)
		requireSameStringEnd(t, raw[:rng.Intn(len(raw))+1], 0)
		requireSameStringEnd(t, append([]byte("[1,"), raw...), 3)
	}
}

// The composite skipper built on the new string scan finds the same end as on the legacy one,
// on valid documents and on arbitrary fragments (unbalanced, unterminated, stray quotes).
func TestScanJSONCompositeEndMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	check := func(body []byte) {
		t.Helper()
		for i := 0; i < len(body); i++ {
			if body[i] != '{' && body[i] != '[' {
				continue
			}
			wantEnd, wantErr := legacyScanJSONCompositeEnd(body, i)
			gotEnd, gotErr := scanJSONCompositeEnd(body, i)
			if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) || wantEnd != gotEnd {
				t.Fatalf("scanJSONCompositeEnd(%q, %d): legacy=(%d, %v) new=(%d, %v)", body, i, wantEnd, wantErr, gotEnd, gotErr)
			}
		}
	}
	for iter := 0; iter < 3000; iter++ {
		doc := []byte(perf18RandJSON(rng, 0))
		check(doc)
		if len(doc) > 1 {
			cut := rng.Intn(len(doc))
			check(doc[:cut])
			mutated := append([]byte(nil), doc...)
			mutated[rng.Intn(len(mutated))] = "\"\\{}[],: a\n"[rng.Intn(11)]
			check(mutated)
		}
	}
	for _, s := range []string{`{"a":"}"}`, `["]"]`, `{"a":"\\"}`, `{"a":"\\\"}"}`, `[{"a":[]},{"b":{}}]`, `{"a":"` + strings.Repeat(`\\`, 5000) + `"}`} {
		check([]byte(s))
	}
}

// perf18RandJSON produces valid JSON with random whitespace and strings full of escapes.
func perf18RandJSON(rng *rand.Rand, depth int) string {
	switch k := rng.Intn(10); {
	case depth < 5 && k < 3:
		var sb strings.Builder
		sb.WriteString("{" + perf18RandWS(rng))
		n := rng.Intn(5)
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteString("," + perf18RandWS(rng))
			}
			sb.WriteString(perf18RandKey(rng) + perf18RandWS(rng) + ":" + perf18RandWS(rng) + perf18RandJSON(rng, depth+1) + perf18RandWS(rng))
		}
		sb.WriteString("}")
		return sb.String()
	case depth < 5 && k < 5:
		var sb strings.Builder
		sb.WriteString("[" + perf18RandWS(rng))
		n := rng.Intn(5)
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteString("," + perf18RandWS(rng))
			}
			sb.WriteString(perf18RandJSON(rng, depth+1) + perf18RandWS(rng))
		}
		sb.WriteString("]")
		return sb.String()
	case k < 8:
		return perf18RandString(rng)
	case k == 8:
		return []string{"0", "-0", "1", "-12", "3.25", "1e10", "-1.5E-3", "4096.7", "9007199254740993", "123456789012345678901234567890", "0.1"}[rng.Intn(11)]
	default:
		return []string{"true", "false", "null"}[rng.Intn(3)]
	}
}

func perf18RandWS(rng *rand.Rand) string {
	if rng.Intn(4) != 0 {
		return ""
	}
	return []string{" ", "\n", "\t", "\r\n  ", "  "}[rng.Intn(5)]
}

var perf18Escapes = []string{`\"`, `\\`, `\/`, `\b`, `\f`, `\n`, `\r`, `\t`, `A`, `é`, "\xe2\x81\xa3", `😀`, `\u0000`, `\\\\`, `\\\"`}

func perf18RandString(rng *rand.Rand) string {
	var sb strings.Builder
	sb.WriteByte('"')
	n := rng.Intn(40)
	if rng.Intn(25) == 0 {
		n = 300 + rng.Intn(3000)
	}
	for i := 0; i < n; i++ {
		switch r := rng.Intn(10); {
		case r < 2:
			sb.WriteString(perf18Escapes[rng.Intn(len(perf18Escapes))])
		case r == 2:
			sb.WriteString([]string{"é", "—", "😀", "中文", "\xe2\x81\xa3", " "}[rng.Intn(6)])
		default:
			sb.WriteByte(byte('a' + rng.Intn(26)))
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func perf18RandKey(rng *rand.Rand) string {
	keys := []string{`"model"`, `"stream"`, `"metadata"`, `"thinking"`, `"output_config"`, `"speed"`, `"max_tokens"`, `"system"`, `"messages"`, `"input"`, `"contents"`, `"systemInstruction"`,
		`"Model"`, `"MODEL"`, `"model"`, `"model"`, `"stream"`, `"parts"`, `"user_id"`, `"type"`, `"effort"`, `"other"`, `"a\"b"`, `"\\"`}
	return keys[rng.Intn(len(keys))]
}

// perf18RandTopLevel builds a request-like object whose hot fields hold random JSON, so type
// errors (model not a string, stream not a bool), duplicates and escaped keys all occur.
func perf18RandTopLevel(rng *rand.Rand) string {
	var sb strings.Builder
	sb.WriteString("{" + perf18RandWS(rng))
	n := rng.Intn(9)
	// metadata / thinking / output_config are read through a nested path, messages / contents only
	// count when they are arrays. With a duplicated key gjson takes the first member (messages /
	// contents) or keeps trying later duplicates (nested paths), while the single-pass scan takes the
	// first array / the first object; that difference predates the scan's string handling and is not
	// what these tests compare, so each of them appears at most once. (Every other hot key keeps its
	// duplicates: first one wins in both.)
	nested := map[string]bool{}
	for i := 0; i < n; i++ {
		key := perf18RandKey(rng)
		switch key {
		case `"metadata"`, `"thinking"`, `"output_config"`, `"messages"`, `"contents"`, `"systemInstruction"`:
			if nested[key] {
				continue
			}
			nested[key] = true
		}
		if i > 0 {
			sb.WriteString("," + perf18RandWS(rng))
		}
		var val string
		switch key {
		case `"model"`:
			val = []string{`"claude-sonnet-4-5"`, `"claude-sonnet-4-5[1m]"`, `"gpt-5"`, perf18RandString(rng), perf18RandJSON(rng, 4)}[rng.Intn(5)]
		case `"stream"`:
			val = []string{`true`, `false`, perf18RandJSON(rng, 4)}[rng.Intn(3)]
		case `"max_tokens"`:
			val = []string{`4096`, `4096.7`, `"7"`, `-1`, `1e3`, `99999999999999999999`, perf18RandJSON(rng, 4)}[rng.Intn(7)]
		case `"metadata"`:
			val = []string{`{"user_id":"u1"}`, `{"user_id":5}`, `{"user_id":"a\"b"}`, perf18RandJSON(rng, 2)}[rng.Intn(4)]
		case `"thinking"`:
			val = []string{`{"type":"enabled"}`, `{"type":"adaptive","budget_tokens":5}`, `{"type":"disabled"}`, perf18RandJSON(rng, 3)}[rng.Intn(4)]
		case `"output_config"`:
			val = []string{`{"effort":" high "}`, `{"effort":null}`, perf18RandJSON(rng, 3)}[rng.Intn(3)]
		case `"systemInstruction"`:
			val = []string{`{"parts":[{"text":"x"}]}`, `{"parts":"no"}`, `{"a":1}`, perf18RandJSON(rng, 3)}[rng.Intn(4)]
		default:
			val = perf18RandJSON(rng, 1+rng.Intn(3))
		}
		sb.WriteString(key + perf18RandWS(rng) + ":" + perf18RandWS(rng) + val + perf18RandWS(rng))
	}
	sb.WriteString("}")
	return sb.String()
}

// ParseGatewayRequest (validity gate + single-pass scan) against the gjson implementation it
// replaced, on random request-shaped documents and on the same documents with one byte changed.
func TestParseGatewayRequestRandomMatchesGJSONLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	protocols := []string{domain.PlatformAnthropic, "", domain.PlatformGemini, "responses"}
	compare := func(body []byte) {
		t.Helper()
		for _, protocol := range protocols {
			protocol := protocol
			defer func() {
				if t.Failed() {
					t.Logf("protocol %q body %q", protocol, body)
				}
			}()
			want := &ParsedRequest{Body: NewRequestBodyRef(append([]byte(nil), body...))}
			wantErr := legacyParse(want, protocol)
			got, gotErr := ParseGatewayRequest(NewRequestBodyRef(append([]byte(nil), body...)), protocol)
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("protocol %q body %q: legacy err=%v new err=%v", protocol, body, wantErr, gotErr)
			}
			if wantErr != nil {
				require.Equal(t, wantErr.Error(), gotErr.Error(), "protocol %q body %q", protocol, body)
				continue
			}
			assertParsedEqual(t, want, got)
			// the raw ranges are byte-exact, not just JSON-equal
			require.True(t, bytes.Equal(want.SystemRaw(), got.SystemRaw()), "SystemRaw %q", body)
			require.True(t, bytes.Equal(want.MessagesRaw(), got.MessagesRaw()), "MessagesRaw %q", body)
			require.True(t, bytes.Equal(want.InputRaw(), got.InputRaw()), "InputRaw %q", body)
		}
	}
	for iter := 0; iter < 4000; iter++ {
		body := []byte(perf18RandTopLevel(rng))
		compare(body)
		if len(body) > 2 {
			mutated := append([]byte(nil), body...)
			mutated[rng.Intn(len(mutated))] = "\"\\{}[],: a1\n"[rng.Intn(12)]
			compare(mutated)
			compare(body[:rng.Intn(len(body))])
		}
	}
	// shapes that are not random: the cases the review asked for by name
	deep := strings.Repeat("[", 500) + strings.Repeat("]", 500)
	for _, body := range []string{
		``, ` `, `null`, `true`, `"str"`, `123`, `[]`, `{}`, `{"model":null}`, `{"model":"a","model":"b"}`, `{"model":"a","Model":"b"}`,
		`{"model":"a","model":"b"}`, "\xef\xbb\xbf" + `{"model":"a"}`, `{"model":"a"} trailing`, `{"model":"a"}{"model":"b"}`,
		`{"model":"a","messages":` + deep + `}`, `{"messages":[{"content":"` + strings.Repeat(`\"}]`, 3000) + `"}]}`,
		`{"model":"a","max_tokens":9223372036854775807}`, `{"model":"a","max_tokens":9223372036854775808}`, `{"model":"a","max_tokens":-9223372036854775808}`,
		`{"model":"a","max_tokens":1e400}`, `{"model":"a","max_tokens":0.0}`, `{"model":"a","max_tokens":-0}`,
		`{"model":"😀😀\ud83d","stream":true}`, `{"model":"` + "\xff\xfe" + `"}`, `{"model":"a` + "\n" + `b"}`,
	} {
		compare([]byte(body))
	}
}
