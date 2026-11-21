package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
)

// Turning a disabled channel on is the only transition that must wait for the
// wallet lock to be recovered. Saving unrelated fields on an already-enabled
// channel stays allowed, so a stale lock cannot freeze the whole config page.
func TestCryptoEnableNeedsUnlockedWallet(t *testing.T) {
	disabled := cryptopay.CryptoConfig{}
	enabled := cryptopay.CryptoConfig{Enabled: true}
	cases := []struct {
		name          string
		before, after cryptopay.CryptoConfig
		want          bool
	}{
		{"disable to enable", disabled, enabled, true},
		{"enable to enable", enabled, enabled, false},
		{"enable to disable", enabled, disabled, false},
		{"disable to disable", disabled, disabled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cryptoEnableNeedsUnlockedWallet(tc.before, tc.after); got != tc.want {
				t.Fatalf("cryptoEnableNeedsUnlockedWallet() = %v, want %v", got, tc.want)
			}
		})
	}
}
