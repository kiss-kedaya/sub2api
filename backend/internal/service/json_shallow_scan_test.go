package service

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// jsonValidCases mixes documents every JSON parser agrees on with the corner
// cases where hand-written validators usually drift from encoding/json.
var jsonValidCases = []string{
	``, ` `, `null`, `true`, `false`, `0`, `-0`, `1`, `-1`, `1.5`, `1e5`, `1E+5`, `1e-5`, `-1.5e+10`,
	`01`, `-`, `1.`, `.5`, `1e`, `1e+`, `+1`, `0x1`, `1.5.3`, `--1`, `1a`, `NaN`, `Infinity`, `-Infinity`,
	`""`, `"a"`, `"\""`, `"\\"`, `"\/"`, `"\b\f\n\r\t"`, `"\u0041"`, `"\u00e9"`, `"\ud800"`, `"\ud800\udc00"`,
	`"\u00G0"`, `"\u00"`, `"\u"`, `"\x41"`, `"\'"`, `"\`, `"abc`, "\"a\x00b\"", "\"a\x1fb\"", "\"a\tb\"", "\"a\nb\"",
	"\"a\x7fb\"", "\"\xff\xfe\"", "\"\xc3\x28\"", `"é"`, `"😀"`,
	`[]`, `[ ]`, `[1]`, `[1,2]`, `[1,]`, `[,1]`, `[1 2]`, `[[]]`, `[{}]`, `[1,[2,[3]]]`, `[`, `]`, `[1`, `[1,`, `[[`, `[]]`,
	`{}`, `{ }`, `{"a":1}`, `{"a":1,}`, `{,"a":1}`, `{"a"}`, `{"a":}`, `{"a" 1}`, `{a:1}`, `{'a':1}`, `{"a":1 "b":2}`,
	`{"a":1,"a":2}`, `{"a":{"b":[1,{"c":null}]}}`, `{`, `}`, `{"a":1`, `{"a":1}}`, `{"a":1}{"b":2}`, `{"a":1} x`,
	` {"a" : [ 1 , 2 ] } `, "\t{\n\"a\"\r:\n1}\n", "\x0b{}", "{}\x0c", "\u00a0{}", "\ufeff{}", "{\"a\":1}\x00",
	`{"a":tru}`, `{"a":truee}`, `{"a":nul}`, `{"a":True}`, `{"a":false}`, `[true,false,null]`,
	`{"a":"b"}   `, `   {"a":"b"}`, `"x" "y"`, `1 2`, `[] []`,
}

func TestValidJSONMatchesStdlibOnCases(t *testing.T) {
	for _, c := range jsonValidCases {
		require.Equalf(t, json.Valid([]byte(c)), validJSON([]byte(c)), "input %q", c)
	}
}

func TestValidJSONDepthLimitMatchesStdlib(t *testing.T) {
	for _, depth := range []int{1, 2, 63, 64, 65, 9999, 10000, 10001, 10002, 20000} {
		for _, open := range []struct{ o, c string }{{"[", "]"}, {`{"a":`, "}"}} {
			doc := strings.Repeat(open.o, depth) + "1" + strings.Repeat(open.c, depth)
			require.Equalf(t, json.Valid([]byte(doc)), validJSON([]byte(doc)), "depth %d open %q", depth, open.o)
		}
	}
	// Unterminated deep input must not recurse or panic either.
	require.False(t, validJSON([]byte(strings.Repeat("[", 1<<20))))
}

// randomJSONValue builds a value from a vocabulary heavy in the tokens the
// validators treat specially.
func randomJSONValue(r *rand.Rand, depth int) string {
	pick := r.Intn(10)
	if depth <= 0 && pick >= 6 {
		pick = r.Intn(6)
	}
	switch pick {
	case 0:
		return []string{"0", "-0", "1", "-1", "12345678901234567890", "1.5", "-0.25", "1e5", "1E-5", "2.5e+10", "0.0e0"}[r.Intn(11)]
	case 1:
		return []string{"true", "false", "null"}[r.Intn(3)]
	case 2:
		return `""`
	case 3, 4, 5:
		return randomJSONString(r)
	case 6, 7:
		n := r.Intn(4)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = randomJSONValue(r, depth-1)
		}
		return "[" + strings.Join(parts, jsonSep(r)) + "]"
	default:
		n := r.Intn(4)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = randomJSONString(r) + jsonColon(r) + randomJSONValue(r, depth-1)
		}
		return "{" + strings.Join(parts, jsonSep(r)) + "}"
	}
}

func jsonSep(r *rand.Rand) string {
	return []string{",", ", ", " ,", ",\n  ", "\t,\t"}[r.Intn(5)]
}

func jsonColon(r *rand.Rand) string {
	return []string{":", ": ", " : ", ":\n"}[r.Intn(4)]
}

func randomJSONString(r *rand.Rand) string {
	frags := []string{
		"a", "b", "call_1", "type", "id", " ", "x y", `\"`, `\\`, `\/`, `\n`, `\t`, `\u0041`, `\u00e9`, `\ud83d\ude00`, `\ud800`,
		"é", "😀", "\u2028", "<", ">", "&", "\x7f", "\xff", "{", "}", "[", "]", ",", ":", "\\\\\\\"",
	}
	n := r.Intn(5)
	var b strings.Builder
	_ = b.WriteByte('"')
	for i := 0; i < n; i++ {
		_, _ = b.WriteString(frags[r.Intn(len(frags))])
	}
	_ = b.WriteByte('"')
	return b.String()
}

func mutateJSON(r *rand.Rand, s string) string {
	b := []byte(s)
	for k := r.Intn(3); k >= 0 && len(b) > 0; k-- {
		i := r.Intn(len(b))
		switch r.Intn(4) {
		case 0:
			b = append(b[:i], b[i+1:]...)
		case 1:
			b[i] = "{}[]\",:\\ 0e-.tfn\x00\n"[r.Intn(18)]
		case 2:
			b = append(b[:i], append([]byte{"{}[]\",:\\ 0e-.tfn"[r.Intn(16)]}, b[i:]...)...)
		default:
			b = b[:i]
		}
	}
	return string(b)
}

func TestValidJSONMatchesStdlibRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 60000; i++ {
		doc := randomJSONValue(r, 4)
		require.Equalf(t, json.Valid([]byte(doc)), validJSON([]byte(doc)), "valid-ish doc %q", doc)
		mutated := mutateJSON(r, doc)
		require.Equalf(t, json.Valid([]byte(mutated)), validJSON([]byte(mutated)), "mutated doc %q", mutated)
	}
}

func FuzzValidJSON(f *testing.F) {
	for _, c := range jsonValidCases {
		f.Add([]byte(c))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if got, want := validJSON(data), json.Valid(data); got != want {
			t.Fatalf("validJSON(%q)=%v, json.Valid=%v", data, got, want)
		}
	})
}

func TestJSONScanHelpers(t *testing.T) {
	t.Run("skip string", func(t *testing.T) {
		for _, tc := range []struct {
			in   string
			end  int
			esc  bool
			isOK bool
		}{
			{`"abc"`, 5, false, true},
			{`""`, 2, false, true},
			{`"a\"b"`, 6, true, true},
			{`"a\\"`, 5, true, true},
			{`"a\\\"b"x`, 8, true, true},
			{`"a\\\\"`, 7, true, true},
			{`"abc`, 4, false, false},
			{`"abc\"`, 6, true, false},
		} {
			end, esc, ok := jsonSkipString(tc.in, 0)
			require.Equalf(t, tc.isOK, ok, "%q", tc.in)
			if ok {
				require.Equalf(t, tc.end, end, "%q", tc.in)
				require.Equalf(t, tc.esc, esc, "%q", tc.in)
			}
		}
	})
	t.Run("members and elements on valid documents", func(t *testing.T) {
		r := rand.New(rand.NewSource(2))
		for i := 0; i < 20000; i++ {
			doc := randomJSONValue(r, 4)
			var generic any
			if json.Unmarshal([]byte(doc), &generic) != nil {
				continue
			}
			switch v := generic.(type) {
			case map[string]any:
				var raw []json.RawMessage
				keys := 0
				ok := jsonForEachMember(doc, func(key, val string) bool {
					keys++
					raw = append(raw, json.RawMessage(val))
					require.Truef(t, json.Valid([]byte(val)), "member value %q of %q", val, doc)
					return true
				})
				if !ok {
					// only allowed when a key is spelled with escapes
					require.Containsf(t, doc, `\`, "walk refused %q", doc)
					continue
				}
				// duplicates are reported separately, so the count can exceed the map size
				require.GreaterOrEqualf(t, keys, len(v), "%q", doc)
			case []any:
				n := 0
				ok := jsonForEachElement(doc, func(elem string) bool {
					n++
					require.Truef(t, json.Valid([]byte(elem)), "element %q of %q", elem, doc)
					return true
				})
				require.Truef(t, ok, "%q", doc)
				require.Equalf(t, len(v), n, "%q", doc)
			}
		}
	})
	t.Run("plain string", func(t *testing.T) {
		for _, tc := range []struct {
			in   string
			want string
			ok   bool
		}{
			{`"call_1"`, "call_1", true},
			{`""`, "", true},
			{`12`, "", true},
			{`null`, "", true},
			{`{"a":1}`, "", true},
			{`"a\nb"`, "", false},
			{`"é"`, "", false},
			{`"a\u0041"`, "", false},
		} {
			got, ok := jsonPlainString(tc.in)
			require.Equalf(t, tc.ok, ok, "%q", tc.in)
			if ok {
				require.Equalf(t, tc.want, got, "%q", tc.in)
			}
		}
	})
}

func BenchmarkValidJSON(b *testing.B) {
	for _, size := range []int{50 << 10, 300 << 10, 1 << 20} {
		body := syntheticResponsesBody(size, false)
		b.Run(byteSizeName(size)+"/stdlib", func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !json.Valid(body) {
					b.Fatal("invalid")
				}
			}
		})
		b.Run(byteSizeName(size)+"/validJSON", func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if !validJSON(body) {
					b.Fatal("invalid")
				}
			}
		})
	}
}
