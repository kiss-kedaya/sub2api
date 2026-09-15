package handler

import (
	"strings"
	"testing"
	"unsafe"
)

func TestOpsUserAgentDoesNotRetainLargeHeader(t *testing.T) {
	for _, value := range []string{
		strings.Repeat("a", 32<<10),
		strings.Repeat(" ", 16<<10) + "short-agent" + strings.Repeat(" ", 16<<10),
	} {
		got := normalizeOpsPersistentUserAgent(value)
		want := truncateString(strings.TrimSpace(value), opsErrorLogMaxUserAgentBytes)
		if got != want {
			t.Fatalf("user agent changed: got %q want %q", got, want)
		}
		start := uintptr(unsafe.Pointer(unsafe.StringData(value)))
		retained := uintptr(unsafe.Pointer(unsafe.StringData(got)))
		if retained >= start && retained < start+uintptr(len(value)) {
			t.Fatalf("%d-byte queued user agent retains a %d-byte header", len(got), len(value))
		}
	}
}
