// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
)

// TrafficEvent contains metering metadata only. Never add bodies, raw URLs or credentials.
type TrafficEvent struct {
	EventID            string    `json:"event_id"`
	UserID             int64     `json:"user_id"`
	APIKeyID           int64     `json:"api_key_id"`
	GroupID            int64     `json:"group_id"`
	RequestID          string    `json:"request_id"`
	Endpoint           string    `json:"endpoint"`
	Method             string    `json:"method"`
	StartedAt          time.Time `json:"started_at"`
	FinishedAt         time.Time `json:"finished_at"`
	UploadBytes        int64     `json:"upload_bytes"`
	DownloadBytes      int64     `json:"download_bytes"`
	StatusCode         int       `json:"status_code"`
	Outcome            string    `json:"outcome"`
	RequestComplete    bool      `json:"request_complete"`
	MeasurementVersion int       `json:"measurement_version"`
}

var ErrInvalidTrafficEvent = errors.New("invalid traffic event")

// ValidateTrafficEvent identifies permanent metadata errors before SQL delivery.
func ValidateTrafficEvent(e TrafficEvent) error {
	if _, err := uuid.Parse(e.EventID); err != nil {
		return ErrInvalidTrafficEvent
	}
	if e.UserID <= 0 || e.APIKeyID <= 0 || e.GroupID < 0 || e.UploadBytes < 0 || e.DownloadBytes < 0 ||
		e.MeasurementVersion != 1 || e.StartedAt.IsZero() || e.FinishedAt.Before(e.StartedAt) {
		return ErrInvalidTrafficEvent
	}
	for _, field := range []struct {
		value string
		limit int
	}{
		{e.RequestID, 64}, {e.Endpoint, 256}, {e.Method, 16}, {e.Outcome, 32},
	} {
		if !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) || utf8.RuneCountInString(field.value) > field.limit {
			return ErrInvalidTrafficEvent
		}
	}
	return nil
}

// TrafficKeyOption is safe for history filters, including soft-deleted keys.
type TrafficKeyOption struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

func (s *UsageService) TrafficKeyOptions(ctx context.Context, userID int64, query string) ([]TrafficKeyOption, error) {
	r, ok := s.usageRepo.(interface {
		TrafficKeyOptions(context.Context, int64, string) ([]TrafficKeyOption, error)
	})
	if !ok {
		return nil, errors.New("traffic key options unavailable")
	}
	return r.TrafficKeyOptions(ctx, userID, query)
}

type TrafficFilter struct {
	UserID, APIKeyID    int64
	Start, End          time.Time
	Period, Granularity string
	Page, PageSize      int
	Location            *time.Location
	View                string // Empty/all preserves the original combined response.
	Cursor              *TrafficCursor
}

// A cursor is a position, never an authorization token. Queries always retain
// the authenticated user and selected key scope, including when a cursor is used.
type TrafficCursor struct {
	StartedAt time.Time `json:"t"`
	EventID   string    `json:"id"`
}

func EncodeTrafficCursor(event TrafficEvent) string {
	raw, _ := json.Marshal(TrafficCursor{StartedAt: event.StartedAt, EventID: event.EventID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func ParseTrafficCursor(value string) (*TrafficCursor, error) {
	if len(value) > 256 {
		return nil, errors.New("invalid traffic cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor TrafficCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.StartedAt.IsZero() {
		return nil, errors.New("invalid traffic cursor")
	}
	id, err := uuid.Parse(cursor.EventID)
	if err != nil {
		return nil, errors.New("invalid traffic cursor")
	}
	cursor.EventID = id.String()
	return &cursor, nil
}

type TrafficTotals struct {
	UploadBytes   int64 `json:"upload_bytes"`
	DownloadBytes int64 `json:"download_bytes"`
	TotalBytes    int64 `json:"total_bytes"`
	Requests      int64 `json:"requests"`
}

type TrafficPoint struct {
	Bucket string `json:"bucket"`
	TrafficTotals
}

type TrafficReport struct {
	TrafficTotals
	Items              []TrafficEvent `json:"items"`
	Trend              []TrafficPoint `json:"trend"`
	HasMore            bool           `json:"has_more"`
	NextCursor         string         `json:"next_cursor,omitempty"`
	Page               int            `json:"page"`
	PageSize           int            `json:"page_size"`
	Period             string         `json:"period"`
	Start              time.Time      `json:"start"`
	End                time.Time      `json:"end"`
	Timezone           string         `json:"timezone"`
	RecordedSince      *time.Time     `json:"recorded_since"`
	MeasurementVersion int            `json:"measurement_version"`
}

type TrafficRepository interface {
	RecordTraffic(context.Context, TrafficEvent) error
	Traffic(context.Context, TrafficFilter) (*TrafficReport, error)
}

// NewTrafficFilter uses calendar periods in the site timezone, with an exclusive
// end. Civil dates advance in UTC, independently of gaps in the local calendar.
func NewTrafficFilter(period, date string, now time.Time) (TrafficFilter, error) {
	return newTrafficFilter(period, date, now, timezone.Location())
}

func newTrafficFilter(period, date string, now time.Time, loc *time.Location) (TrafficFilter, error) {
	civil := trafficCivilDate(now.In(loc))
	if date != "" {
		var err error
		// Parse date fields without converting a nonexistent local midnight to
		// the previous day (for example Santiago's spring DST transition).
		civil, err = time.Parse("2006-01-02", date)
		if err != nil {
			return TrafficFilter{}, errors.New("invalid date, use YYYY-MM-DD")
		}
	}
	f := TrafficFilter{Period: period, Page: 1, PageSize: 20, Granularity: "day", Location: loc}
	start, end := civil, civil.AddDate(0, 0, 1)
	switch period {
	case "day":
		f.Granularity = "hour"
	case "week":
		start = civil.AddDate(0, 0, -(int(civil.Weekday())+6)%7)
		end = start.AddDate(0, 0, 7)
	case "month":
		start = time.Date(civil.Year(), civil.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, 0)
	case "year":
		start = time.Date(civil.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		end, f.Granularity = start.AddDate(1, 0, 0), "month"
	default:
		return TrafficFilter{}, errors.New("invalid period, use day/week/month/year")
	}
	f.Start, f.End = trafficDayBoundary(start, loc), trafficDayBoundary(end, loc)
	if !f.End.After(f.Start) {
		return TrafficFilter{}, errors.New("selected calendar period does not exist in the site timezone")
	}
	return f, nil
}

// UTC is only a container for civil date fields, not the time of the boundary.
func trafficCivilDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// trafficDayBoundary returns the earliest midnight, or the first instant after
// a skipped midnight/day. Checking adjacent zone intervals handles both folds
// and gaps; time.Date alone may normalize a gap backwards to the previous day.
func trafficDayBoundary(civil time.Time, loc *time.Location) time.Time {
	var best time.Time
	found := false
	consider := func(t time.Time) {
		if !found || t.Before(best) {
			best = t
			found = true
		}
	}
	limit := civil.Add(48 * time.Hour)
	for probe := civil.Add(-48 * time.Hour).In(loc); !probe.After(limit); {
		start, end := probe.ZoneBounds()
		_, offset := probe.Zone()
		midnight := civil.Add(-time.Duration(offset) * time.Second)
		if (start.IsZero() || !midnight.Before(start)) && (end.IsZero() || midnight.Before(end)) {
			consider(midnight)
		}
		if !start.IsZero() && !start.UTC().Add(time.Duration(offset)*time.Second).Before(civil) {
			consider(start)
		}
		if end.IsZero() || !end.After(probe) {
			break
		}
		probe = end.In(loc)
	}
	if found {
		return best.In(loc)
	}
	// Fixed-offset locations always have a valid midnight; retain a bounded
	// fallback for locations whose transitions lie outside the search window.
	return time.Date(civil.Year(), civil.Month(), civil.Day(), 0, 0, 0, 0, loc)
}

type TrafficBucket struct {
	Label      string
	Start, End time.Time
}

func TrafficBuckets(f TrafficFilter) []TrafficBucket {
	loc := f.Location
	if loc == nil {
		loc = timezone.Location()
	}
	var buckets []TrafficBucket
	start := f.Start.In(loc)
	if f.Granularity == "hour" {
		for start.Before(f.End) {
			end := start.Add(time.Hour)
			if end.After(f.End) {
				end = f.End
			}
			if !end.After(start) {
				break
			}
			buckets = append(buckets, TrafficBucket{start.Format("2006-01-02 15:04 -07:00"), start, end})
			start = end
		}
		return buckets
	}
	// The cursor always advances by Gregorian dates in UTC. A skipped civil
	// date may have an empty interval, but can never hold this loop in place.
	limit := trafficCivilDate(f.End.In(loc)).AddDate(0, 0, 1)
	for civil := trafficCivilDate(start); civil.Before(limit) && start.Before(f.End); {
		next := civil.AddDate(0, 0, 1)
		label := civil.Format("2006-01-02")
		if f.Granularity == "month" {
			next = civil.AddDate(0, 1, 0)
			label = civil.Format("2006-01")
		}
		end := trafficDayBoundary(next, loc)
		if end.After(f.End) {
			end = f.End
		}
		if end.After(start) {
			buckets = append(buckets, TrafficBucket{label, start, end})
			start = end
		}
		civil = next
	}
	return buckets
}

func (s *UsageService) RecordTraffic(ctx context.Context, event TrafficEvent) error {
	r, ok := s.usageRepo.(TrafficRepository)
	if !ok {
		return errors.New("traffic repository unavailable")
	}
	return r.RecordTraffic(ctx, event)
}

func (s *UsageService) RecordTrafficBatch(ctx context.Context, events []TrafficEvent) error {
	r, ok := s.usageRepo.(interface {
		RecordTrafficBatch(context.Context, []TrafficEvent) error
	})
	if !ok {
		return errors.New("traffic batch repository unavailable")
	}
	return r.RecordTrafficBatch(ctx, events)
}

func (s *UsageService) Traffic(ctx context.Context, filter TrafficFilter) (*TrafficReport, error) {
	r, ok := s.usageRepo.(TrafficRepository)
	if !ok {
		return nil, errors.New("traffic repository unavailable")
	}
	return r.Traffic(ctx, filter)
}
