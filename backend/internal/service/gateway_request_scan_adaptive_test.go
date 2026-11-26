//go:build unit

package service

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stringEndTuning is the tuning of scanJSONStringEndWith.
type stringEndTuning struct{ head, hit, limit, run int }

// stringEndTunings drives every switch point of scanJSONStringEndWith with values small enough for short
// strings to reach them, next to the production values.
var stringEndTunings = func() []stringEndTuning {
	var all []stringEndTuning
	for _, head := range []int{0, 1, 2, 3, 5} {
		for _, hit := range []int{1, 4, stringEndHitCost} {
			for _, limit := range []int{0, 2, 6, stringEndDebtLimit} {
				for _, run := range []int{1, 2, 3, stringEndMaxRun} {
					all = append(all, stringEndTuning{head, hit, limit, run})
				}
			}
		}
	}
	return append(all, stringEndTuning{stringEndPlainHead, stringEndHitCost, stringEndDebtLimit, stringEndMaxRun})
}()

var stringEndProduction = stringEndTuning{stringEndPlainHead, stringEndHitCost, stringEndDebtLimit, stringEndMaxRun}

// scanJSONStringEndTuned is scanJSONStringEnd with every tuning value passed in: the same head loop (head bytes),
// then the production long phase with the given values. gaveUp tells how the closing quote was found.
func scanJSONStringEndTuned(body []byte, i, head, hitCost, debtLimit, maxRun int) (end int, gaveUp bool, err error) {
	j := i + 1
	plainEnd := min(j+head, len(body))
	for ; j < plainEnd; j++ {
		switch body[j] {
		case '\\':
			j++
		case '"':
			return j + 1, false, nil
		}
	}
	return scanJSONStringEndLongWith(body, i, j, hitCost, debtLimit, maxRun)
}

// requireSame checks the result against the legacy byte loop, error text included, and returns gaveUp. With the
// production tuning the production function itself is checked too.
func (tn stringEndTuning) requireSame(t *testing.T, body []byte, i int) bool {
	t.Helper()
	wantEnd, wantErr := legacyScanJSONStringEnd(body, i)
	gotEnd, gaveUp, gotErr := scanJSONStringEndTuned(body, i, tn.head, tn.hit, tn.limit, tn.run)
	if (wantErr == nil) != (gotErr == nil) || (wantErr != nil && wantErr.Error() != gotErr.Error()) || wantEnd != gotEnd {
		t.Fatalf("%+v scanJSONStringEndTuned(%q, %d): legacy=(%d, %v) new=(%d, %v)", tn, body, i, wantEnd, wantErr, gotEnd, gotErr)
	}
	if tn == stringEndProduction {
		prodEnd, prodErr := scanJSONStringEnd(body, i)
		if (wantErr == nil) != (prodErr == nil) || (wantErr != nil && wantErr.Error() != prodErr.Error()) || wantEnd != prodEnd {
			t.Fatalf("scanJSONStringEnd(%q, %d): legacy=(%d, %v) new=(%d, %v)", body, i, wantEnd, wantErr, prodEnd, prodErr)
		}
	}
	return gaveUp
}

// Every string of up to eight bytes over {quote, backslash, ordinary byte}, with every combination of small
// tuning values, so each switch point (end of the head, debt over the limit, run over the cap, the closing
// quote being the last byte of the body, a trailing backslash) is crossed by some string.
func TestScanJSONStringEndAdaptiveExhaustiveMatchesLegacy(t *testing.T) {
	alphabet := []byte{'"', '\\', 'a'}
	var walk func(content []byte, depth int)
	walk = func(content []byte, depth int) {
		body := append([]byte{'"'}, content...)
		for _, tn := range stringEndTunings {
			tn.requireSame(t, body, 0)
		}
		if depth == 0 {
			return
		}
		for _, c := range alphabet {
			walk(append(content[:len(content):len(content)], c), depth-1)
		}
	}
	walk(nil, 8)
}

// perf18DenseContent builds string content out of pieces that stress the switch: plain runs of every length
// around the head, escaped quotes close together and far apart, backslash runs around the run cap followed by
// a quote, and the occasional quote that really ends the string.
func perf18DenseContent(rng *rand.Rand) string {
	var sb strings.Builder
	for n := rng.Intn(40); n > 0; n-- {
		switch rng.Intn(9) {
		case 0, 1:
			sb.WriteString(strings.Repeat("a", rng.Intn(70)))
		case 2, 3:
			sb.WriteString(`\"`)
		case 4:
			sb.WriteString(`\\`)
		case 5:
			sb.WriteString(`\n`)
		case 6, 7:
			sb.WriteString(strings.Repeat(`\`, rng.Intn(stringEndMaxRun+4)) + `"`)
		default:
			sb.WriteString(`"`)
		}
	}
	return sb.String()
}

func TestScanJSONStringEndAdaptiveRandomDenseMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	for iter := 0; iter < 150000; iter++ {
		prefix := strings.Repeat(`\`, rng.Intn(4)) + strings.Repeat("a", rng.Intn(3))
		body := []byte(prefix + `"` + perf18DenseContent(rng))
		switch rng.Intn(4) {
		case 0:
			body = append(body, `"`...)
		case 1:
			body = append(body, `","next":"x"}`...)
		case 2:
			if len(body) > 0 {
				body = body[:rng.Intn(len(body))+1]
			}
		}
		i := len(prefix)
		if i >= len(body) {
			continue
		}
		stringEndProduction.requireSame(t, body, i)
		stringEndTunings[rng.Intn(len(stringEndTunings))].requireSame(t, body, i)
	}
}

// Production tuning around its boundaries: a string whose end is just before, at and just after the end of the
// head, escaped quotes at every spacing around the hit cost, and runs of backslashes around the cap.
func TestScanJSONStringEndAdaptiveBoundariesMatchLegacy(t *testing.T) {
	var bodies []string
	for n := 0; n <= 70; n++ {
		bodies = append(bodies, `"`+strings.Repeat("a", n)+`"`, `"`+strings.Repeat("a", n)+`\"`, `"`+strings.Repeat("a", n)+`\`)
	}
	for _, gap := range []int{0, 1, 2, 5, 10, 22, 23, 24, 25, 26, 40, 100, 300} {
		for m := 0; m <= 9; m++ {
			for _, lead := range []int{0, 5, stringEndPlainHead - 1, stringEndPlainHead, stringEndPlainHead + 1, 90} {
				content := strings.Repeat("a", lead) + strings.Repeat(strings.Repeat("a", gap)+`\"`, m)
				bodies = append(bodies, `"`+content+`"`, `"`+content+strings.Repeat("a", gap)+`"tail`, `"`+content)
			}
		}
	}
	for run := 0; run <= stringEndMaxRun+4; run++ {
		for _, lead := range []int{0, 7, stringEndPlainHead, 60} {
			tail := strings.Repeat("a", lead) + strings.Repeat(`\`, run) + `"`
			bodies = append(bodies, `"`+tail, `"`+tail+`"`, `"`+tail+`x"`, `"`+tail+strings.Repeat(`\`, run)+`"`)
		}
	}
	for _, b := range bodies {
		stringEndProduction.requireSame(t, []byte(b), 0)
		stringEndProduction.requireSame(t, []byte("ab"+b), 2)
	}
}

// The adaptive switch does not change results, so only gaveUp shows that it happens, when and where it should.
func TestScanJSONStringEndAdaptiveSwitchPoints(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a", n) }
	bs := func(n int) string { return strings.Repeat(`\`, n) }
	cases := []struct {
		name    string
		content string
		gaveUp  bool
	}{
		{"short string never leaves the head", a(10) + `\"` + a(5), false},
		{"plain long text", a(500), false},
		{"one escaped quote near the head is affordable", a(40) + `\"` + a(40), false},
		{"two escaped quotes close together are not", a(40) + `\"\"` + a(40), true},
		{"escaped quotes far apart stay on IndexByte", strings.Repeat(a(100)+`\"`, 12), false},
		{"every escaped quote a few bytes after the last is dense", a(40) + `\"` + a(3) + `\"` + a(3) + `\"` + a(40), true},
		{"sparse quotes pay the debt back", a(40) + `\"` + a(200) + `\"` + a(200) + `\"` + a(200), false},
		{"debt is never below zero", a(40) + `\"` + a(300) + `\"` + `\"` + a(10), true},
		{"the closing quote right after a dense stretch is still found", a(40) + `\"\"`, true},
		{"debt exactly at the limit stays", a(34) + `\"` + a(40), false},
		{"debt one over the limit gives up", a(33) + `\"` + a(40), true},
		{"even run at the cap is counted", a(40) + bs(stringEndMaxRun), false},
		{"odd run below the cap is counted", a(40) + bs(stringEndMaxRun-1) + `"` + a(3), false},
		{"odd run over the cap is not counted back", a(40) + bs(stringEndMaxRun+1) + `"` + a(3), true},
		{"even run over the cap ends the string like any other", a(40) + bs(stringEndMaxRun+2), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, closing := range []string{`"`, `"tail`} {
				body := []byte(`"` + c.content + closing)
				require.Equal(t, c.gaveUp, stringEndProduction.requireSame(t, body, 0), "closing %q", closing)
			}
		})
	}
}
