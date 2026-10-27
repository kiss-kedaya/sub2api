package googleapi

import (
	"net/http"
	"testing"
)

func TestHTTPStatusToGoogleStatusPreservesRetryableUnavailable(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusTooManyRequests:     "RESOURCE_EXHAUSTED",
		http.StatusServiceUnavailable:  "UNAVAILABLE",
		http.StatusInternalServerError: "INTERNAL",
		http.StatusBadGateway:          "INTERNAL",
	} {
		if got := HTTPStatusToGoogleStatus(status); got != want {
			t.Errorf("status %d: got %q, want %q", status, got, want)
		}
	}
}
