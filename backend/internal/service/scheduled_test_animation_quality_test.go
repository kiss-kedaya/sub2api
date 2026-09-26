package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func qualityFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "scheduled_quality", name+".html"))
	require.NoError(t, err)
	return string(b)
}

func TestScheduledQualityStructuralRegressions(t *testing.T) {
	placement := qualityFixture(t, "placement-conflict")
	crank := qualityFixture(t, "crank-corner")
	wheels := strings.Replace(placement, ".leg", ".wheel", 1)
	wheels = strings.Replace(wheels, "50% 8%", "center", 1)
	wheels = strings.Replace(wheels, "<g class=\"leg\" transform=\"translate(482 397)\"><path d=\"M0 0L19 45L-4 62\"/></g>", "<g class=\"wheel\"><circle cx=\"100\" cy=\"200\" r=\"40\"/><circle cx=\"300\" cy=\"200\" r=\"40\"/></g>", 1)
	tests := []struct{ name, content, status, reason string }{
		{"r1408239 placement", placement, "degraded", "overrides SVG translation"},
		{"r1408240 crank", crank, "degraded", "stationary hub"},
		{"merged wheel hubs", wheels, "degraded", "one rotating SVG group"},
		{"parent translation is safe", "", "unknown", ""},
		{"origin corrected", strings.Replace(crank, "transform-origin: 0 0", "transform-origin: center", 1), "unknown", "no proven structural defect"},
		{"origin zero but hinge", strings.Replace(crank, "M504 505l34 31M504 505l-34-29", "M504 505l34 31", 1), "unknown", ""},
		{"script may fix placement", strings.Replace(placement, "</body>", "<script>requestAnimationFrame(()=>{})</script></body>", 1), "unknown", "rendered"},
		{"inline disables animation", strings.Replace(placement, "class=\"leg\"", "class=\"leg\" style=\"animation: none\"", 1), "unknown", ""},
		{"id override disables animation", strings.Replace(strings.Replace(placement, "class=\"leg\"", "id=\"leg\" class=\"leg\"", 1), "</style>", "#leg{animation:none}</style>", 1), "unknown", ""},
		{"additive composition", strings.Replace(placement, "transform-box: fill-box;", "animation-composition: add; transform-box: fill-box;", 1), "unknown", ""},
		{"complex selector", strings.Replace(placement, ".leg {", "svg > .leg {", 1), "unknown", "outside supported"},
		{"variable transform", strings.Replace(placement, "rotate(360deg)", "translate(var(--x),var(--y)) rotate(360deg)", 1), "unknown", ""},
		{"paused animation", strings.Replace(placement, "linear infinite", "linear infinite paused", 1), "unknown", ""},
		{"zero duration", strings.Replace(placement, ".72s", "0s", 1), "unknown", ""},
		{"conditional axis override", strings.Replace(crank, "</style>", "@media screen{.pedal-system{transform-origin:center}}</style>", 1), "unknown", "outside supported"},
		{"comments do not override", strings.Replace(placement, "</style>", "/* .leg { animation: none } */</style>", 1), "degraded", "overrides SVG translation"},
	}
	// Move the placement transform to a parent, preserving the same rotation.
	tests[3].content = strings.Replace(strings.Replace(placement, "<g class=\"leg\" transform=\"translate(482 397)\">", "<g transform=\"translate(482 397)\"><g class=\"leg\">", 1), "</g>", "</g></g>", 1)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, reason := assessScheduledTestQuality(tt.content, DefaultScheduledTestPrompt)
			require.Equal(t, tt.status, status, reason)
			require.Contains(t, reason, tt.reason)
		})
	}
}

func TestScheduledQualityRigidPathLegs(t *testing.T) {
	content := `<html><style>.leg{animation:swing 1s linear infinite}@keyframes swing{0%,100%{transform:rotate(0deg)}50%{transform:rotate(28deg)}}</style><svg><path class="leg" d="M582 395L604 465L650 504"/><path class="leg" d="M646 394L674 452L650 504"/></svg></html>`
	status, reason := assessScheduledTestQuality(content, "")
	require.Equal(t, "degraded", status, reason)
	require.Contains(t, reason, "rigid shape")
	straight := strings.ReplaceAll(content, "L604 465", "")
	straight = strings.ReplaceAll(straight, "L674 452", "")
	status, reason = assessScheduledTestQuality(straight, "")
	require.Equal(t, "unknown", status, reason)
}

func TestScheduledQualityReducedMotionCondition(t *testing.T) {
	content := qualityFixture(t, "placement-conflict")
	for _, tc := range []struct{ query, status string }{
		{"(prefers-reduced-motion: reduce)", "degraded"},
		{"(prefers-reduced-motion: no-preference)", "unknown"},
		{"screen and (prefers-reduced-motion: reduce)", "unknown"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			input := strings.Replace(content, "</style>", "@media "+tc.query+"{.leg{animation:none}}</style>", 1)
			status, reason := assessScheduledTestQuality(input, "")
			require.Equal(t, tc.status, status, reason)
		})
	}
}

func TestScheduledQualityUnknownIsNotSuccess(t *testing.T) {
	for _, content := range []string{
		"<!doctype html><html><body><svg></svg><script>requestAnimationFrame(()=>{})</script></body></html>",
		"<!doctype html><html><body><svg><circle><animate attributeName=\"cx\"/></circle></svg></body></html>",
	} {
		status, _ := assessScheduledTestQuality(content, DefaultScheduledTestPrompt)
		require.Equal(t, "unknown", status)
	}
	status, _ := assessScheduledTestQuality("pong", "Reply pong")
	require.Equal(t, "unknown", status, "custom prompt must not be graded as HTML")
	status, _ = assessScheduledTestQuality(strings.Repeat("x", 1024*1024+1), "")
	require.Equal(t, "unknown", status)
}

func TestScheduledQualityDocumentRegressions(t *testing.T) {
	for _, content := range []string{
		"<html><body><svg><path d=\"M0 0", // truncated production outputs
		"<html><svg><!-- </svg></html> -->",
		"<html><svg><script>const text='</svg></html>';</script>",
		"<html><svg><svg></svg></html>",
		"<html><svg><animate/></svg><script src = '//example.com/a.js'></script></html>",
	} {
		status, _ := assessScheduledTestQuality(content, "")
		require.Equal(t, "degraded", status)
	}
}

func TestScheduledQualityProductionSamples(t *testing.T) {
	dir := os.Getenv("SCHEDULED_QUALITY_SAMPLE_DIR")
	if dir == "" {
		t.Skip("optional local production samples")
	}
	for name, want := range map[string]string{"r1408239.html": "degraded", "r1408240.html": "degraded", "r1408279.html": "unknown"} {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			status, reason := assessScheduledTestQuality(string(b), "")
			t.Log(status, reason)
			require.Equal(t, want, status, reason)
		})
	}
}
