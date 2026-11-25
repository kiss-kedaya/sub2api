package service

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func legacyNormalizeCompatSeedJSON(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var tmp any
	if err := json.Unmarshal(v, &tmp); err != nil {
		return string(v)
	}
	out, err := json.Marshal(tmp)
	if err != nil {
		return string(v)
	}
	return string(out)
}

// bigRandomJSON returns a JSON array whose text is around size bytes, built from
// the same hostile vocabulary the validator tests use (escapes, unicode, big
// numbers, unsorted and duplicate keys).
func bigRandomJSON(r *rand.Rand, size int) string {
	var b strings.Builder
	b.WriteByte('[')
	for b.Len() < size {
		if b.Len() > 1 {
			b.WriteString(jsonSep(r))
		}
		b.WriteString(randomJSONValue(r, 3))
	}
	b.WriteByte(']')
	return b.String()
}

func TestNormalizeCompatSeedJSONMatchesLegacy(t *testing.T) {
	r := rand.New(rand.NewSource(21))
	for i := 0; i < 3000; i++ {
		size := []int{0, 20, 200, 511, 512, 513, 2000, 20000}[r.Intn(8)]
		doc := bigRandomJSON(r, size)
		if r.Intn(4) == 0 {
			doc = mutateJSON(r, doc)
		}
		if r.Intn(20) == 0 {
			doc = ""
		}
		raw := json.RawMessage(doc)
		want := legacyNormalizeCompatSeedJSON(raw)
		for round := 0; round < 3; round++ { // miss, then hits
			require.Equalf(t, want, normalizeCompatSeedJSON(raw), "round %d size %d doc %.200s", round, size, doc)
		}
	}
}

func TestNormalizeCompatSeedJSONLargeAndErrorInputs(t *testing.T) {
	big := json.RawMessage(`{"b":1,"a":"` + strings.Repeat("x", compatSeedMemoMaxItem) + `"}`)
	require.Equal(t, legacyNormalizeCompatSeedJSON(big), normalizeCompatSeedJSON(big))
	for _, doc := range []string{
		`{"b":1e999,"a":` + strings.Repeat("1", 600) + `}`,                        // out of float64 range: returned as is
		`[` + strings.Repeat(`{"k":[`, 12000) + strings.Repeat(`]}`, 12000) + `]`, // deeper than the decoder allows
		`{"z":1,"a":2}` + strings.Repeat(" ", 600),
		`{"html":"<>& ","n":-0,"f":1.50,"e":1E+3,"u":"\ud800","k":"é","a":"A"}` + strings.Repeat(" ", 600),
	} {
		raw := json.RawMessage(doc)
		require.Equal(t, legacyNormalizeCompatSeedJSON(raw), normalizeCompatSeedJSON(raw))
		require.Equal(t, legacyNormalizeCompatSeedJSON(raw), normalizeCompatSeedJSON(raw))
	}
}

func TestCompatSeedMemoEvictionNeverReturnsWrongValue(t *testing.T) {
	// Tiny budget: constant rotation. Every answer must still equal the legacy
	// result, and the memo must stay inside its budget.
	memo := newCompatSeedMemo(2, 8<<10)
	r := rand.New(rand.NewSource(22))
	docs := make([]string, 40)
	for i := range docs {
		docs[i] = bigRandomJSON(r, 700+r.Intn(1500))
	}
	for i := 0; i < 4000; i++ {
		doc := docs[r.Intn(len(docs))]
		raw := []byte(doc)
		out, ok := memo.get(raw)
		if !ok {
			out = legacyNormalizeCompatSeedJSON(raw)
			memo.put(raw, out)
		}
		require.Equal(t, legacyNormalizeCompatSeedJSON(raw), out)
	}
	for i := range memo.shards {
		s := &memo.shards[i]
		require.LessOrEqual(t, s.curBytes, s.maxBytes)
		total := 0
		for k, v := range s.cur {
			total += len(k) + len(v)
		}
		require.Equal(t, s.curBytes, total)
	}
}

func TestCompatSeedMemoConcurrent(t *testing.T) {
	memo := newCompatSeedMemo(4, 64<<10)
	r := rand.New(rand.NewSource(23))
	docs := make([]string, 25)
	want := make([]string, len(docs))
	for i := range docs {
		docs[i] = bigRandomJSON(r, 600+r.Intn(900))
		want[i] = legacyNormalizeCompatSeedJSON([]byte(docs[i]))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rr := rand.New(rand.NewSource(seed))
			for i := 0; i < 3000; i++ {
				k := rr.Intn(len(docs))
				raw := []byte(docs[k])
				out, ok := memo.get(raw)
				if !ok {
					out = legacyNormalizeCompatSeedJSON(raw)
					memo.put(raw, out)
				}
				if out != want[k] {
					t.Errorf("wrong memo answer for doc %d", k)
					return
				}
			}
		}(int64(g))
	}
	wg.Wait()
}

func BenchmarkNormalizeCompatSeedJSON(b *testing.B) {
	tools := func(n, salt int) json.RawMessage {
		var sb strings.Builder
		sb.WriteString("[")
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"type":"function","function":{"name":"tool_%d_%d","description":"does thing number %d with a longer description text","parameters":{"type":"object","properties":{"path":{"type":"string","description":"file path"},"limit":{"type":"integer"}},"required":["path"]}}}`, salt, i, i)
		}
		sb.WriteString("]")
		return json.RawMessage(sb.String())
	}
	for _, n := range []int{4, 20, 100} {
		raw := tools(n, 0)
		name := fmt.Sprintf("%dB", len(raw))
		b.Run(name+"/legacy", func(b *testing.B) {
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				legacyNormalizeCompatSeedJSON(raw)
			}
		})
		b.Run(name+"/new-repeat", func(b *testing.B) {
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				normalizeCompatSeedJSON(raw)
			}
		})
		b.Run(name+"/new-miss", func(b *testing.B) {
			// Distinct inputs against a memo too small to hold them: every call is a
			// miss, so this is the legacy cost plus the memo bookkeeping.
			variants := make([]json.RawMessage, 4096)
			for i := range variants {
				variants[i] = tools(n, i+1)
			}
			memo := newCompatSeedMemo(compatSeedMemoShards, 1<<20)
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v := variants[i%len(variants)]
				if _, ok := memo.get(v); !ok {
					memo.put(v, normalizeCompatSeedJSONUncached(v))
				}
			}
		})
	}
}
