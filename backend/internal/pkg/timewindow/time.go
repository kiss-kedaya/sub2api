// Package timewindow shares strict wall-clock and IANA-zone parsing between
// recurring schedules and channel time pricing.
package timewindow

import (
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

var locations sync.Map

// LoadLocation rejects implicit host-local time and caches validated IANA
// locations for the request and scheduler hot paths.
func LoadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("an explicit IANA timezone is required")
	}
	if cached, ok := locations.Load(name); ok {
		if location, valid := cached.(*time.Location); valid && location != nil {
			return location, nil
		}
		locations.Delete(name)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	actual, _ := locations.LoadOrStore(name, location)
	loaded, ok := actual.(*time.Location)
	if !ok || loaded == nil {
		return nil, fmt.Errorf("invalid cached timezone %q", name)
	}
	return loaded, nil
}

// ParseClock accepts the same HH:mm / HH:mm:ss syntax as channel pricing.
// For an end boundary, midnight denotes the end of the local day.
func ParseClock(value string, endBoundary bool) (int, error) {
	if endBoundary && (value == "00:00" || value == "00:00:00") {
		return 24 * 60 * 60, nil
	}
	layout := "15:04:05"
	if len(value) == len("15:04") {
		layout = "15:04"
	}
	parsed, err := time.Parse(layout, value)
	if err != nil || parsed.Format(layout) != value {
		return 0, fmt.Errorf("time %q must use HH:mm or HH:mm:ss format", value)
	}
	return parsed.Hour()*3600 + parsed.Minute()*60 + parsed.Second(), nil
}
