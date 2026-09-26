package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func qualityMorphCheck(t *testing.T, content string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return qualityMorphFailure(doc)
}

const morphRotor = `<style>
.spin { transform-box: fill-box; transform-origin: center; animation: rotateCrank 1.25s linear infinite }
@keyframes rotateCrank { to { transform: rotate(360deg) } }
</style><svg><g class="spin" stroke="black" stroke-width="2"><circle cx="50" cy="50" r="4"/>
<path d="M50 50 L80 50 M50 50 L20 50"/><path d="M80 50 h10 M20 50 h-10"/></g><g fill="none" stroke="black" stroke-width="2">%s</g></svg>`

func TestQualityMorphSMILChord(t *testing.T) {
	leg := `<path d="M40 0 L60 25 L80 50"><animate attributeName="d" dur="1.25s" repeatCount="indefinite" values="M40 0 L60 25 L80 50;M40 0 L50 25 L20 50;M40 0 L60 25 L80 50"/></path>`
	if got := qualityMorphCheck(t, "<html>"+strings.Replace(morphRotor, "%s", leg, 1)+"</html>"); !strings.Contains(got, "pedal orbit") {
		t.Fatalf("expected proven chord defect, got %q", got)
	}
	for _, tt := range []struct{ name, leg string }{
		{"different cycle", strings.Replace(leg, `dur="1.25s"`, `dur="2s"`, 1)},
		{"unrelated morph", strings.Replace(leg, "L20 50", "L85 55", 1)},
		{"stationary path", `<path d="M40 10 L80 50"/>`},
		{"discrete interpolation", strings.Replace(leg, `attributeName="d"`, `attributeName="d" calcMode="discrete"`, 1)},
		{"spline interpolation", strings.Replace(leg, `attributeName="d"`, `attributeName="d" calcMode="spline" keySplines="0 0 1 1;0 0 1 1"`, 1)},
		{"paced interpolation", strings.Replace(leg, `attributeName="d"`, `attributeName="d" calcMode="paced"`, 1)},
		{"explicit key times", strings.Replace(leg, `attributeName="d"`, `attributeName="d" keyTimes="0;0.2;1"`, 1)},
		{"delayed start", strings.Replace(leg, `attributeName="d"`, `attributeName="d" begin="2s"`, 1)},
		{"ancestor translation", `<g transform="translate(200 0)">` + leg + `</g>`},
		{"ancestor scaling", `<g style="transform:scale(2)">` + leg + `</g>`},
		{"script document", `<script>document.querySelector('path').remove()</script>` + leg},
		{"inline handler", `<g onload="void 0">` + leg + `</g>`},
		{"hidden path", strings.Replace(leg, `<path `, `<path display="none" `, 1)},
		{"unsupported path command", strings.ReplaceAll(leg, `L60 25`, `A10 10 0 0 1 60 25`)},
		{"incompatible path commands", strings.Replace(leg, `M40 0 L50 25 L20 50`, `M40 0 Q50 25 20 50`, 1)},
		{"near circular samples", `<path d="M40 0 L60 25 L80 50"><animate attributeName="d" dur="1.25s" repeatCount="indefinite" values="M40 0 L60 25 L80 50;M40 0 L60 25 L50 80;M40 0 L60 25 L20 50;M40 0 L60 25 L50 20;M40 0 L60 25 L80 50"/></path>`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := qualityMorphCheck(t, "<html>"+strings.Replace(morphRotor, "%s", tt.leg, 1)+"</html>")
			if got != "" {
				t.Fatalf("unproven defect: %s", got)
			}
		})
	}
}

func TestQualityMorphCSSStaticFeet(t *testing.T) {
	const css = `<style>
.spin { transform-box: fill-box; transform-origin: center; animation: turn 1s linear infinite }
.left { animation: morphLeft 1s ease-in-out infinite }
.right { animation: morphRight 1s ease-in-out infinite }
@keyframes turn { to { transform: rotate(360deg) } }
@keyframes morphLeft { 0%, 100% { d: path("M40 10 Q30 30 20 50") } 50% { d: path("M40 10 Q50 30 80 50") } }
@keyframes morphRight { 0%, 100% { d: path("M60 10 Q70 30 80 50") } 50% { d: path("M60 10 Q50 30 20 50") } }
</style>`
	const scene = `<html>` + css + `<svg><g class="spin" stroke="black" stroke-width="2"><circle cx="50" cy="50" r="4"/><path d="M50 50 L80 50"/><path d="M50 50 L20 50"/><path d="M80 50 h10 M20 50 h-10"/></g>
<path class="left" fill="none" stroke="black" d="M40 10 Q30 30 20 50"/><path class="right" fill="none" stroke="black" d="M60 10 Q70 30 80 50"/>
<path d="M15 46 l15 0 0 8 -15 0Z"/><path d="M75 46 l15 0 0 8 -15 0Z"/></svg></html>`
	if got := qualityMorphCheck(t, scene); !strings.Contains(got, "foot") {
		t.Fatalf("expected static foot defect, got %q", got)
	}
	for _, tt := range []struct{ name, scene string }{
		{"no static shoes", strings.Replace(scene, `<path d="M15 46 l15 0 0 8 -15 0Z"/>`, `<path d="M15 46 l15 0 0 8 -15 0Z"><animateTransform attributeName="transform" type="translate" from="0 0" to="60 0" dur="1s" repeatCount="indefinite"/></path>`, 1)},
		{"no rotating crank", strings.Replace(scene, `class="spin"`, `class="still"`, 1)},
		{"wrong morph period", strings.Replace(scene, `.left { animation: morphLeft 1s`, `.left { animation: morphLeft 2s`, 1)},
		{"paused morph", strings.Replace(scene, `morphLeft 1s ease-in-out infinite`, `morphLeft 1s ease-in-out infinite paused`, 1)},
		{"paused rotor", strings.Replace(scene, `turn 1s linear infinite`, `turn 1s linear infinite paused`, 1)},
		{"CSS animated shoe", strings.Replace(scene, `<path d="M15 46`, `<path class="left" d="M15 46`, 1)},
		{"script", strings.Replace(scene, `<svg>`, `<script>void 0</script><svg>`, 1)},
		{"different SVG", strings.Replace(scene, `</g>`, `</g></svg><svg>`, 1)},
		{"ancestor rotate", strings.Replace(scene, `<svg>`, `<svg><g transform="rotate(45)">`, 1)},
		{"conditional animation override", strings.Replace(scene, `</style>`, `@media (min-width:1px) { .left { animation:none } }</style>`, 1)},
		{"middle shoe", strings.Replace(scene, `</svg>`, `<path d="M45 45 h10 v10 h-10Z"/></svg>`, 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualityMorphCheck(t, tt.scene); got != "" {
				t.Fatalf("unproven defect: %s", got)
			}
		})
	}
}

func TestQualityMorphProductionSamples(t *testing.T) {
	dir := os.Getenv("SCHEDULED_QUALITY_SAMPLE_DIR")
	if dir == "" {
		t.Skip("optional local samples")
	}
	for _, name := range []string{"a29173-r1408478.html", "a29173-r1408976.html", "a29173-r1408831.html"} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			got := qualityMorphCheck(t, string(content))
			if got == "" {
				t.Fatal("expected proven linkage defect")
			}
			status, reason := assessScheduledTestQuality(string(content), "")
			if status != "degraded" {
				t.Fatalf("main evaluator status=%s reason=%s", status, reason)
			}
			t.Log(got)
			for _, mutation := range []struct{ name, content string }{
				{"script guard", strings.Replace(string(content), "</head>", "<script>void 0</script></head>", 1)},
				{"ancestor transform guard", strings.Replace(string(content), "<svg ", `<svg transform="scale(2)" `, 1)},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					if got := qualityMorphCheck(t, mutation.content); got != "" {
						t.Fatalf("unproven defect: %s", got)
					}
				})
			}
		})
	}
}

func TestQualityMorphPathParser(t *testing.T) {
	for _, tt := range []struct {
		path  string
		end   qualityMorphPoint
		valid bool
	}{
		{"M10 20 L30 40", qualityMorphPoint{30, 40}, true},
		{"M10 20 l30 40", qualityMorphPoint{40, 60}, true},
		{"M10 20 q20-10 37 1 l14 8 q-2 11-15 10", qualityMorphPoint{46, 39}, true},
		{"M10 20 C20 20 30 30 40 40 50 50 60 60 70 70", qualityMorphPoint{70, 70}, true},
		{"M10 20 L30 40Z", qualityMorphPoint{10, 20}, true},
		{"M10 20 X30 40", qualityMorphPoint{}, false},
		{"M10 20 L30", qualityMorphPoint{}, false},
		{"M10 20 LNaN 40", qualityMorphPoint{}, false},
		{"M10 20 L1e999 40", qualityMorphPoint{}, false},
	} {
		t.Run(tt.path, func(t *testing.T) {
			p, ok := qualityMorphParsePath(tt.path)
			if ok != tt.valid || (ok && qualityMorphDistance(p.end, tt.end) > .001) {
				t.Fatalf("valid=%v end=%+v", ok, p.end)
			}
		})
	}
}
