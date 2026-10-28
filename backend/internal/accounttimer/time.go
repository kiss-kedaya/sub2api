// Package accounttimer implements schedulable account timing without model calls or browser timers.
package accounttimer

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Error struct{ Code string }

func (e *Error) Error() string { return "account timer: " + e.Code }
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "internal"
}
func invalid(code string) error { return &Error{Code: code} }

type Policy struct {
	MaxDelay  time.Duration
	OpenGrace time.Duration
}

func DefaultPolicy() Policy { return Policy{366 * 24 * time.Hour, 15 * time.Minute} }

const PollInterval = 30 * time.Second

type Input struct {
	Mode         string   `json:"mode"`
	Hours        int64    `json:"hours"`
	Minutes      int64    `json:"minutes"`
	Date         string   `json:"date"`
	Time         string   `json:"time"`
	Timezone     string   `json:"timezone"`
	WeekdaysOnly bool     `json:"weekdays_only"`
	Periods      []Period `json:"periods"`
}
type Period struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
type Preview struct {
	ExecuteAt time.Time `json:"execute_at"`
	Action    string    `json:"action,omitempty"`
	Timezone  string    `json:"timezone"`
	LocalDate string    `json:"local_date"`
	LocalTime string    `json:"local_time"`
	Offset    string    `json:"offset"`
	ServerNow time.Time `json:"server_now"`
}

// ConfiguredPolicy bounds the operator-configurable planning horizon. Invalid values
// fail closed to the documented default; the maximum remains ten years.
func ConfiguredPolicy() Policy {
	p := DefaultPolicy()
	if days, err := strconv.Atoi(os.Getenv("ACCOUNT_SCHEDULABLE_TIMER_MAX_DAYS")); err == nil && days >= 1 && days <= 3660 {
		p.MaxDelay = time.Duration(days) * 24 * time.Hour
	}
	return p
}
func Resolve(in Input, now time.Time, p Policy) (Preview, error) {
	var out Preview
	if in.Timezone == "" || in.Timezone == "Local" || (in.Timezone != "UTC" && !strings.Contains(in.Timezone, "/")) {
		return out, invalid("timezone")
	}
	loc, err := time.LoadLocation(in.Timezone)
	if err != nil {
		return out, invalid("timezone")
	}
	var due time.Time
	switch in.Mode {
	case "relative":
		maxMinutes := int64(p.MaxDelay / time.Minute)
		if in.Date != "" || in.Time != "" || in.Hours < 0 || in.Minutes < 0 || in.Minutes > maxMinutes || in.Hours > maxMinutes/60 {
			return out, invalid("duration")
		}
		minutes := in.Hours*60 + in.Minutes
		if minutes <= 0 || minutes > maxMinutes {
			return out, invalid("duration")
		}
		due = now.Add(time.Duration(minutes) * time.Minute)
	case "absolute":
		if in.Hours != 0 || in.Minutes != 0 {
			return out, invalid("datetime")
		}
		date := in.Date
		if date == "" {
			date = now.In(loc).Format("2006-01-02")
		}
		due, err = resolveWall(date, in.Time, loc)
		if err != nil {
			return out, err
		}
		if in.Date == "" && !due.After(now) {
			day, _ := time.Parse("2006-01-02", date)
			due, err = resolveWall(day.AddDate(0, 0, 1).Format("2006-01-02"), in.Time, loc)
			if err != nil {
				return out, err
			}
		}
	case "recurring":
		due, action, err := resolveRecurring(in, now, loc)
		if err != nil {
			return out, err
		}
		local := due.In(loc)
		return Preview{ExecuteAt: due.UTC(), Action: action, Timezone: in.Timezone, LocalDate: local.Format("2006-01-02"), LocalTime: local.Format("15:04"), Offset: local.Format("-07:00"), ServerNow: now.UTC()}, nil
	default:
		return out, invalid("mode")
	}
	if !due.After(now) {
		return out, invalid("past")
	}
	if due.Sub(now) > p.MaxDelay {
		return out, invalid("duration")
	}
	local := due.In(loc)
	return Preview{ExecuteAt: due.UTC(), Timezone: in.Timezone, LocalDate: local.Format("2006-01-02"), LocalTime: local.Format("15:04"), Offset: local.Format("-07:00"), ServerNow: now.UTC()}, nil
}

type recurringSegment struct{ day, start, end int }

func parseClock(value string) (int, error) {
	t, err := time.Parse("15:04", value)
	if err != nil || t.Format("15:04") != value {
		return 0, invalid("period")
	}
	return t.Hour()*60 + t.Minute(), nil
}

func validateRecurringPeriods(in Input) error {
	if len(in.Periods) == 0 || len(in.Periods) > 24 {
		return invalid("period")
	}
	segments := make([]recurringSegment, 0, len(in.Periods)*2*7)
	for _, period := range in.Periods {
		start, err := parseClock(period.Start)
		if err != nil {
			return err
		}
		end, err := parseClock(period.End)
		if err != nil || start == end {
			return invalid("period")
		}
		for day := 0; day < 7; day++ {
			if in.WeekdaysOnly && day >= 5 {
				continue
			}
			if end > start {
				segments = append(segments, recurringSegment{day: day, start: start, end: end})
				continue
			}
			segments = append(segments, recurringSegment{day: day, start: start, end: 24 * 60})
			segments = append(segments, recurringSegment{day: (day + 1) % 7, start: 0, end: end})
		}
	}
	for day := 0; day < 7; day++ {
		daySegments := make([]recurringSegment, 0)
		for _, segment := range segments {
			if segment.day == day {
				daySegments = append(daySegments, segment)
			}
		}
		sort.Slice(daySegments, func(i, j int) bool { return daySegments[i].start < daySegments[j].start })
		for i := 1; i < len(daySegments); i++ {
			if daySegments[i].start < daySegments[i-1].end {
				return invalid("overlap")
			}
		}
	}
	return nil
}

func resolveRecurring(in Input, now time.Time, loc *time.Location) (time.Time, string, error) {
	if err := validateRecurringPeriods(in); err != nil {
		return time.Time{}, "", err
	}
	localNow := now.In(loc)
	base := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	var best time.Time
	bestAction := ""
	for offset := -1; offset <= 8; offset++ {
		day := base.AddDate(0, 0, offset)
		weekday := int(day.Weekday())
		if in.WeekdaysOnly && (weekday == int(time.Saturday) || weekday == int(time.Sunday)) {
			continue
		}
		for _, period := range in.Periods {
			start, _ := parseClock(period.Start)
			end, _ := parseClock(period.End)
			endDay := day
			if end < start {
				endDay = day.AddDate(0, 0, 1)
			}
			for _, transition := range []struct {
				date   time.Time
				clock  int
				action string
			}{{day, start, "open"}, {endDay, end, "close"}} {
				date := transition.date.Format("2006-01-02")
				clock := fmt.Sprintf("%02d:%02d", transition.clock/60, transition.clock%60)
				candidate, err := resolveWall(date, clock, loc)
				if err != nil {
					if ErrorCode(err) == "dst" {
						continue
					}
					return time.Time{}, "", err
				}
				if !candidate.After(now) || (!best.IsZero() && !candidate.Before(best)) {
					continue
				}
				best, bestAction = candidate, transition.action
			}
		}
	}
	if best.IsZero() {
		return time.Time{}, "", invalid("recurring")
	}
	return best, bestAction, nil
}

// recurringActive reports whether the account should be schedulable at now.
// The weekday flag applies to the day on which a period starts; an overnight
// period therefore remains active after midnight even when the following day
// is a weekend.
func recurringActive(in Input, now time.Time, loc *time.Location) (bool, error) {
	if err := validateRecurringPeriods(in); err != nil {
		return false, err
	}
	local := now.In(loc)
	minute := local.Hour()*60 + local.Minute()
	weekday := local.Weekday()
	previous := local.AddDate(0, 0, -1)
	for _, period := range in.Periods {
		start, _ := parseClock(period.Start)
		end, _ := parseClock(period.End)
		if end > start {
			if in.WeekdaysOnly && (weekday == time.Saturday || weekday == time.Sunday) {
				continue
			}
			if minute >= start && minute < end {
				return true, nil
			}
			continue
		}
		if minute >= start {
			if !in.WeekdaysOnly || (weekday >= time.Monday && weekday <= time.Friday) {
				return true, nil
			}
		}
		if minute < end && (!in.WeekdaysOnly || (previous.Weekday() >= time.Monday && previous.Weekday() <= time.Friday)) {
			return true, nil
		}
	}
	return false, nil
}

// Enumerate offsets around the requested wall date, then round-trip every
// candidate. time.Date alone silently normalizes gaps and chooses one fold.
func resolveWall(date, clock string, loc *time.Location) (time.Time, error) {
	text := date + " " + clock
	wall, err := time.Parse("2006-01-02 15:04", text)
	if err != nil || wall.Format("2006-01-02 15:04") != text {
		return time.Time{}, invalid("datetime")
	}
	offsets := map[int]bool{}
	for t := wall.Add(-48 * time.Hour); !t.After(wall.Add(48 * time.Hour)); t = t.Add(30 * time.Minute) {
		_, off := t.In(loc).Zone()
		offsets[off] = true
	}
	matches := []time.Time{}
	for off := range offsets {
		candidate := wall.Add(-time.Duration(off) * time.Second)
		if candidate.In(loc).Format("2006-01-02 15:04") == text {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return time.Time{}, invalid("dst")
	}
	return matches[0], nil
}

// Opening changes only schedulable, and never makes an inactive/expired account
// eligible. Quota, cooldown, ACL and quality gates remain untouched.
func Decision(open bool, due, now time.Time, status string, expires *time.Time, deleted bool, p Policy) string {
	if deleted {
		return "account_deleted"
	}
	if open {
		if now.Sub(due) > p.OpenGrace {
			return "open_expired"
		}
		if status != "active" {
			return "account_inactive"
		}
		if expires != nil && !expires.After(now) {
			return "account_expired"
		}
	}
	return ""
}
