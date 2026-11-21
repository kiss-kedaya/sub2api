package service

import "strings"

// hasPredictableCustomKeyPattern rejects obvious repetitions, not every weak
// key. The server-generated credential is the recommended secure default.
// Do not apply this creation policy when authenticating an existing key.
func hasPredictableCustomKeyPattern(key, configuredPrefix string) bool {
	value := key
	if configuredPrefix != "" && strings.HasPrefix(value, configuredPrefix) {
		value = value[len(configuredPrefix):]
	} else {
		value = strings.TrimPrefix(value, "sk-")
	}
	if value == "" {
		return true
	}

	// Compare adjacent repeats. This also rejects a long repeat with a single
	// different byte, rather than treating that byte as sufficient variation.
	allowedDifferences := 0
	if len(value) >= 32 {
		allowedDifferences = 2
	}
	for period := 1; period <= min(16, len(value)/3); period++ {
		differences := 0
		for i := period; i < len(value); i++ {
			if value[i] != value[i-period] {
				differences++
				if differences > allowedDifferences {
					break
				}
			}
		}
		if differences <= allowedDifferences {
			return true
		}
	}
	return false
}
