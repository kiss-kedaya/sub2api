package service

import (
	"encoding/json"
	"net/mail"
	"strings"
)

// NotifyEmailEntry represents a notification email with enable/disable and verification state.
// Primary is true for the current account-bound email. The primary entry is derived
// from users.email, cannot be removed, and has its disabled state stored separately.
type NotifyEmailEntry struct {
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
	Verified bool   `json:"verified"`
	Primary  bool   `json:"primary,omitempty"`
}

// IsValidNotifyEmail reports whether an account email can be used as a notification
// recipient. Synthetic/reserved sign-in addresses are intentionally excluded.
func IsValidNotifyEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" || isReservedEmail(email) {
		return false
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return false
	}
	at := strings.LastIndexByte(email, '@')
	return at > 0 && at < len(email)-1 && strings.Contains(email[at+1:], ".")
}

// BuildNotifyEmailEntries returns the account-bound email first, followed by the
// existing extra notification emails. The primary entry is always verified and
// inherits its persisted disabled state; duplicate extras are omitted.
func BuildNotifyEmailEntries(user *User) []NotifyEmailEntry {
	if user == nil {
		return nil
	}
	entries := make([]NotifyEmailEntry, 0, len(user.BalanceNotifyExtraEmails)+1)
	seen := make(map[string]struct{}, len(user.BalanceNotifyExtraEmails)+1)
	if IsValidNotifyEmail(user.Email) {
		primary := strings.TrimSpace(user.Email)
		entries = append(entries, NotifyEmailEntry{
			Email: primary, Disabled: user.BalanceNotifyPrimaryEmailDisabled,
			Verified: true, Primary: true,
		})
		seen[strings.ToLower(primary)] = struct{}{}
	}
	for _, entry := range user.BalanceNotifyExtraEmails {
		email := strings.TrimSpace(entry.Email)
		if email == "" {
			continue
		}
		key := strings.ToLower(email)
		if _, ok := seen[key]; ok {
			continue
		}
		entry.Email = email
		entry.Primary = false
		entries = append(entries, entry)
		seen[key] = struct{}{}
	}
	return entries
}

// parseNotifyEmails parses a JSON string into []NotifyEmailEntry.
// It auto-detects the format:
//   - Old format ["email1","email2"] → converted to [{email, disabled:false, verified:true}, ...]
//   - New format [{email,disabled,verified}, ...] → parsed directly
//
// Returns nil on empty/invalid input.
func ParseNotifyEmails(raw string) []NotifyEmailEntry {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}

	// Try parsing as new format first (array of objects)
	var entries []NotifyEmailEntry
	if err := json.Unmarshal([]byte(raw), &entries); err == nil && len(entries) > 0 {
		// Verify it's actually the new format by checking the first element
		// json.Unmarshal into []NotifyEmailEntry succeeds even for ["string"]
		// because it tries to fit "string" into NotifyEmailEntry and gets zero values.
		// We need to detect old format explicitly.
		if !isOldStringArrayFormat(raw) {
			return entries
		}
	}

	// Try parsing as old format (array of strings)
	var emails []string
	if err := json.Unmarshal([]byte(raw), &emails); err == nil {
		result := make([]NotifyEmailEntry, 0, len(emails))
		for _, e := range emails {
			e = strings.TrimSpace(e)
			if e != "" {
				result = append(result, NotifyEmailEntry{
					Email:    e,
					Disabled: false,
					Verified: false, // Old format emails default to unverified
				})
			}
		}
		return result
	}

	return nil
}

// isOldStringArrayFormat checks if the JSON is a string array like ["email1","email2"].
func isOldStringArrayFormat(raw string) bool {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return false
	}
	// Check if first element starts with a quote (string) vs { (object)
	first := strings.TrimSpace(string(arr[0]))
	return len(first) > 0 && first[0] == '"'
}

// marshalNotifyEmails serializes []NotifyEmailEntry to JSON string.
func MarshalNotifyEmails(entries []NotifyEmailEntry) string {
	if len(entries) == 0 {
		return "[]"
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(data)
}
