package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// RequestBodyTop keys its per-request index by the identity (data pointer and length)
// of the body slice, not by its content. A body handed to it must therefore never be
// rewritten in place afterwards: no copy into it, no append(buf[:0], ...) over it, no
// sjson ReplaceInPlace, no pooled buffer refilled with the next body. Every rewrite in
// the gateway returns a new slice, and this test keeps it that way: it greps the
// gateway sources for the idioms that rewrite a byte slice in place and fails on an
// unannotated one. It is a tripwire, not a proof (it only knows body-named slices);
// its job is to make whoever adds such an idiom read this paragraph.
//
// An idiom that cannot reach RequestBodyTop carries "bodytop-ok: <why>" in a comment on
// its line or in the comment block directly above it.

const bodyTopOKMarker = "bodytop-ok:"

type bodyTopIdiom struct {
	name string
	re   *regexp.Regexp
	// allow lists whole files whose matches are known not to carry request bodies.
	allow map[string]string
}

var bodyTopIdioms = []bodyTopIdiom{
	{name: "sjson ReplaceInPlace rewrites its input", re: regexp.MustCompile(`ReplaceInPlace`)},
	{name: "append over a body-named slice", re: regexp.MustCompile(`append\(\s*([A-Za-z_][A-Za-z0-9_.]*)?[Bb]ody[A-Za-z0-9_]*\[`)},
	{name: "copy into a body-named slice", re: regexp.MustCompile(`copy\(\s*([A-Za-z_][A-Za-z0-9_.]*)?[Bb]ody[A-Za-z0-9_]*\[`)},
	{name: "sync.Pool of buffers", re: regexp.MustCompile(`sync\.Pool`), allow: map[string]string{
		"sse_scanner_buffer_pool.go": "SSE line scanner buffers, not request bodies",
		"ops_error_logger.go":        "capture writer state, not request bodies",
	}},
}

// bodyTopAnnotated reports whether line i carries the marker itself or sits right
// below a comment block that does.
func bodyTopAnnotated(lines []string, i int) bool {
	if strings.Contains(lines[i], bodyTopOKMarker) {
		return true
	}
	for j := i - 1; j >= 0 && strings.HasPrefix(strings.TrimSpace(lines[j]), "//"); j-- {
		if strings.Contains(lines[j], bodyTopOKMarker) {
			return true
		}
	}
	return false
}

// bodyTopViolations returns "file:line: idiom: source" for every unannotated match
// in src (comment-only lines are skipped).
func bodyTopViolations(file, src string) []string {
	var out []string
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, idiom := range bodyTopIdioms {
			if !idiom.re.MatchString(line) {
				continue
			}
			if _, ok := idiom.allow[filepath.Base(file)]; ok {
				continue
			}
			if bodyTopAnnotated(lines, i) {
				continue
			}
			out = append(out, file+":"+strconv.Itoa(i+1)+": "+idiom.name+": "+trimmed)
		}
	}
	return out
}

func TestRequestBodyTopContractNoInPlaceBodyRewrites(t *testing.T) {
	var files []string
	for _, dir := range []string{".", filepath.Join("..", "handler")} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, f := range matches {
			if !strings.HasSuffix(f, "_test.go") {
				files = append(files, f)
			}
		}
	}
	// The guard must actually be reading the gateway: service and handler hold
	// hundreds of files, so a handful means the directories moved.
	require.GreaterOrEqual(t, len(files), 100, "the guard found too few sources to read")

	var violations []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		violations = append(violations, bodyTopViolations(f, string(src))...)
	}
	require.Empty(t, violations,
		"a byte slice is rewritten in place. RequestBodyTop (body_top_index.go) answers from an index keyed by the slice's identity, "+
			"so a body that was ever passed to it must never be modified in place. If this slice cannot reach RequestBodyTop, "+
			"say so with a `bodytop-ok: <why>` comment on the line or the comment block above it; otherwise build a new slice")
}

// The guard only protects if its patterns match what they claim to; these lines are
// the shapes it must catch and the ones it must leave alone.
func TestRequestBodyTopContractGuardPatterns(t *testing.T) {
	flagged := []string{
		"	body = append(body[:0], next...)",
		"	grokIntentSourceBody = append(grokIntentSourceBody[:0], body...)",
		"	out := append(requestBody[:i], tail...)",
		"	n := copy(forwardBody[start:], patch)",
		"	opts := &sjson.Options{ReplaceInPlace: true}",
		"	var pool = sync.Pool{New: func() any { return new([]byte) }}",
	}
	for _, line := range flagged {
		require.NotEmpty(t, bodyTopViolations("x.go", line), "must flag: %q", line)
		require.Empty(t, bodyTopViolations("x.go", "// bodytop-ok: scratch\n"+line), "annotation above must clear: %q", line)
		require.Empty(t, bodyTopViolations("x.go", "// bodytop-ok: scratch\n// more words\n"+line), "annotation in the comment block above must clear: %q", line)
		require.Empty(t, bodyTopViolations("x.go", line+" // bodytop-ok: scratch"), "annotation on the line must clear: %q", line)
		require.NotEmpty(t, bodyTopViolations("x.go", "// bodytop-ok: scratch\nx := 1\n"+line), "an annotation above other code does not count: %q", line)
	}
	clean := []string{
		"	out = append(out, body...)",
		"	normalized := append([]byte(nil), body...)",
		"	// body = append(body[:0], next...) in a comment",
		"	payload = append(payload[:0], data...)",
		"	copy(dst, body)",
	}
	for _, line := range clean {
		require.Empty(t, bodyTopViolations("x.go", line), "must not flag: %q", line)
	}
	require.Empty(t, bodyTopViolations("sse_scanner_buffer_pool.go", "var p = sync.Pool{}"), "known pool files are allowed")
}
