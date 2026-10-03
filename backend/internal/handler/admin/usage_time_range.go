package admin

import (
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// parseUsageDateBoundary keeps date-only clients compatible and accepts local
// second-precision timestamps in the user's timezone. SQL uses [start, end),
// so an explicitly selected end second includes that entire second.
func parseUsageDateBoundary(value, userTZ string, end bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if len(value) == len("2006-01-02") {
		t, err := timezone.ParseInUserLocation("2006-01-02", value, userTZ)
		if err == nil && end {
			t = t.AddDate(0, 0, 1)
		}
		return t, err
	}
	var parsed time.Time
	var err error
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		parsed, err = timezone.ParseInUserLocation(layout, value, userTZ)
		if err == nil {
			if parsed.Nanosecond() != 0 {
				return time.Time{}, fmt.Errorf("time precision must not exceed seconds")
			}
			if end {
				parsed = parsed.Add(time.Second)
			}
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date/time, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss: %w", err)
}
