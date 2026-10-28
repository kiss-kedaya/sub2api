package accounttimer

import (
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	now := time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC)
	cases := []struct {
		name  string
		input Input
		want  string
		code  string
	}{
		{"8h from server receipt", Input{Mode: "relative", Hours: 8, Timezone: "Asia/Shanghai"}, "2026-10-22T07:30:00Z", ""},
		{"minutes cross midnight", Input{Mode: "relative", Minutes: 45, Timezone: "Asia/Shanghai"}, "2026-10-22T00:15:00Z", ""},
		{"Beijing next 8", Input{Mode: "absolute", Time: "08:00", Timezone: "Asia/Shanghai"}, "2026-10-22T00:00:00Z", ""},
		{"explicit past stays past", Input{Mode: "absolute", Date: "2026-10-21", Time: "08:00", Timezone: "Asia/Shanghai"}, "", "past"},
		{"explicit Beijing date", Input{Mode: "absolute", Date: "2026-10-23", Time: "08:00", Timezone: "Asia/Shanghai"}, "2026-10-23T00:00:00Z", ""},
		{"invalid zone", Input{Mode: "relative", Hours: 8, Timezone: "Mars/City"}, "", "timezone"},
		{"no implicit zone", Input{Mode: "relative", Hours: 8}, "", "timezone"},
		{"DST missing", Input{Mode: "absolute", Date: "2027-03-14", Time: "02:30", Timezone: "America/New_York"}, "", "dst"},
		{"DST duplicate", Input{Mode: "absolute", Date: "2026-11-01", Time: "01:30", Timezone: "America/New_York"}, "", "dst"},
		{"zero", Input{Mode: "relative", Timezone: "UTC"}, "", "duration"},
		{"extreme", Input{Mode: "relative", Hours: 1 << 60, Timezone: "UTC"}, "", "duration"},
		{"negative", Input{Mode: "relative", Hours: -1, Minutes: 90, Timezone: "UTC"}, "", "duration"},
		{"invalid calendar date", Input{Mode: "absolute", Date: "2026-02-30", Time: "08:00", Timezone: "UTC"}, "", "datetime"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve(c.input, now, DefaultPolicy())
			if c.code != "" {
				if ErrorCode(err) != c.code {
					t.Fatalf("want %s, got %v", c.code, err)
				}
				return
			}
			if err != nil || got.ExecuteAt.Format(time.RFC3339) != c.want {
				t.Fatalf("got %+v %v want %s", got, err, c.want)
			}
			if got.Timezone != c.input.Timezone || got.LocalDate == "" || got.Offset == "" {
				t.Fatal("missing explicit zone preview")
			}
		})
	}
}
func TestNextEightCrossesLocalDay(t *testing.T) {
	now := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	p, err := Resolve(Input{Mode: "absolute", Time: "08:00", Timezone: "Asia/Shanghai"}, now, DefaultPolicy())
	if err != nil || p.ExecuteAt.Format(time.RFC3339) != "2026-10-23T00:00:00Z" {
		t.Fatalf("%+v %v", p, err)
	}
}
func TestRecurringResolutionWeekdaysAndCrossMidnight(t *testing.T) {
	now := time.Date(2026, 10, 24, 16, 30, 0, 0, time.UTC) // Sunday 00:30 in Shanghai
	preview, err := Resolve(Input{Mode: "recurring", Timezone: "Asia/Shanghai", WeekdaysOnly: true, Periods: []Period{{Start: "23:00", End: "01:00"}}}, now, DefaultPolicy())
	if err != nil || preview.Action != "open" || preview.LocalDate != "2026-10-26" || preview.LocalTime != "23:00" {
		t.Fatalf("unexpected recurring preview: %+v %v", preview, err)
	}
}

func TestRecurringWorkdayOvernightWindowClosesOnSaturday(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Mode: "recurring", Timezone: "Asia/Shanghai", WeekdaysOnly: true, Periods: []Period{{Start: "23:00", End: "01:00"}}}
	for _, tc := range []struct {
		at     time.Time
		active bool
	}{
		{time.Date(2026, 10, 23, 15, 30, 0, 0, time.UTC), true},  // Fri 23:30
		{time.Date(2026, 10, 23, 16, 30, 0, 0, time.UTC), true},  // Sat 00:30; Friday occurrence continues
		{time.Date(2026, 10, 23, 17, 0, 0, 0, time.UTC), false},  // Sat 01:00
		{time.Date(2026, 10, 25, 15, 30, 0, 0, time.UTC), false}, // Sunday window cannot start
	} {
		got, err := recurringActive(in, tc.at, loc)
		if err != nil || got != tc.active {
			t.Fatalf("at %s active=%t err=%v want %t", tc.at, got, err, tc.active)
		}
	}
}

func TestRecurringPreviewSkipsNoopBoundaryBetweenAdjacentPeriods(t *testing.T) {
	now := time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC) // 11:00 Shanghai
	preview, err := Resolve(Input{Mode: "recurring", Timezone: "Asia/Shanghai", Periods: []Period{{Start: "09:00", End: "12:00"}, {Start: "12:00", End: "18:00"}}}, now, DefaultPolicy())
	if err != nil || preview.Action != "close" || preview.LocalTime != "18:00" {
		t.Fatalf("adjacent periods must not preview a false transition: %+v %v", preview, err)
	}
}

func TestRecurringPreviewRejectsPlanWithNoFutureStateChange(t *testing.T) {
	input := Input{Mode: "recurring", Timezone: "UTC", Periods: []Period{{Start: "00:00", End: "12:00"}, {Start: "12:00", End: "00:00"}}}
	_, err := Resolve(input, time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC), DefaultPolicy())
	if ErrorCode(err) != "recurring" {
		t.Fatalf("a continuous all-day schedule has no next transition to preview: %v", err)
	}
}

func TestRecurringDstGapAndFoldAreSkippedByPreviewAndExecution(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name               string
		input              Input
		now                time.Time
		invalidOccurrence  time.Time
		wantDate, wantTime string
	}{
		{
			name:              "spring gap",
			input:             Input{Mode: "recurring", Timezone: "America/New_York", Periods: []Period{{Start: "02:30", End: "03:30"}}},
			now:               time.Date(2027, 3, 14, 6, 0, 0, 0, time.UTC),
			invalidOccurrence: time.Date(2027, 3, 14, 7, 0, 0, 0, time.UTC),
			wantDate:          "2027-03-15", wantTime: "02:30",
		},
		{
			name:              "fall fold",
			input:             Input{Mode: "recurring", Timezone: "America/New_York", Periods: []Period{{Start: "01:30", End: "02:30"}}},
			now:               time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC),
			invalidOccurrence: time.Date(2026, 11, 1, 6, 45, 0, 0, time.UTC),
			wantDate:          "2026-11-02", wantTime: "01:30",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preview, err := Resolve(tc.input, tc.now, DefaultPolicy())
			if err != nil || preview.LocalDate != tc.wantDate || preview.LocalTime != tc.wantTime {
				t.Fatalf("unexpected preview: %+v %v", preview, err)
			}
			active, err := recurringActive(tc.input, tc.invalidOccurrence, loc)
			if err != nil || active {
				t.Fatalf("invalid DST occurrence must remain inactive, active=%t err=%v", active, err)
			}
		})
	}
}

func TestRecurringPeriodsRejectOverlapAndEqualBounds(t *testing.T) {
	for name, periods := range map[string][]Period{
		"equal":   {{Start: "08:00", End: "08:00"}},
		"overlap": {{Start: "08:00", End: "10:00"}, {Start: "09:00", End: "11:00"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(Input{Mode: "recurring", Timezone: "UTC", Periods: periods}, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), DefaultPolicy())
			if ErrorCode(err) != "period" && ErrorCode(err) != "overlap" {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
func TestRecurringPeriodsRejectDSTAmbiguousTransition(t *testing.T) {
	preview, err := Resolve(Input{Mode: "recurring", Timezone: "America/New_York", Periods: []Period{{Start: "01:30", End: "02:30"}}}, time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC), DefaultPolicy())
	if err != nil || preview.LocalTime != "01:30" {
		t.Fatalf("expected a valid pre-transition occurrence, got %+v %v", preview, err)
	}
}
func TestExecutionPolicy(t *testing.T) {
	now := time.Now()
	p := DefaultPolicy()
	if Decision(true, now.Add(-16*time.Minute), now, "active", nil, false, p) != "open_expired" {
		t.Fatal("late open must expire")
	}
	if Decision(false, now.Add(-365*24*time.Hour), now, "active", nil, false, p) != "" {
		t.Fatal("late close must run")
	}
	if Decision(true, now, now, "disabled", nil, false, p) != "account_inactive" {
		t.Fatal("must preserve disabled")
	}
	expired := now.Add(-time.Hour)
	if Decision(true, now, now, "active", &expired, false, p) != "account_expired" {
		t.Fatal("must preserve expiry")
	}
	if Decision(false, now, now, "active", nil, true, p) != "account_deleted" {
		t.Fatal("must preserve deletion")
	}
}
