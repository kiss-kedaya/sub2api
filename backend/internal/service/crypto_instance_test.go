package service

import "testing"

func TestCryptoInstanceGatesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		value              string
		channel, adminOnly bool
	}{
		{"", true, false}, {"true", true, true}, {"1", true, true},
		{"false", false, false}, {"0", false, false}, {"malformed", false, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", tc.value)
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", tc.value)
			if CryptoInstanceEnabled() != tc.channel || CryptoAdminTestMode() != tc.adminOnly {
				t.Fatal("deployment gate accepted an unsafe value")
			}
		})
	}
}
