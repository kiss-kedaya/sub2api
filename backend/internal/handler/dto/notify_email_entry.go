package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

// NotifyEmailEntry represents a notification email with enable/disable and verification state.
// Primary is the derived account-bound email and is not accepted as a writable extra.
type NotifyEmailEntry struct {
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
	Verified bool   `json:"verified"`
	Primary  bool   `json:"primary,omitempty"`
}

// NotifyEmailEntriesFromService converts service entries to DTO entries.
func NotifyEmailEntriesFromService(entries []service.NotifyEmailEntry) []NotifyEmailEntry {
	if entries == nil {
		return nil
	}
	result := make([]NotifyEmailEntry, len(entries))
	for i, e := range entries {
		result[i] = NotifyEmailEntry{
			Email:    e.Email,
			Disabled: e.Disabled,
			Verified: e.Verified,
			Primary:  e.Primary,
		}
	}
	return result
}

// NotifyEmailEntriesToService converts DTO entries to service entries.
func NotifyEmailEntriesToService(entries []NotifyEmailEntry) []service.NotifyEmailEntry {
	if entries == nil {
		return nil
	}
	result := make([]service.NotifyEmailEntry, len(entries))
	for i, e := range entries {
		result[i] = service.NotifyEmailEntry{
			Email:    e.Email,
			Disabled: e.Disabled,
			Verified: e.Verified,
		}
	}
	return result
}
