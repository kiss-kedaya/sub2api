package mode

import "testing"

func TestReviewWriteGateRequiresExplicitValidOptIn(t *testing.T) {
	for _, tc := range []struct {
		test, write        string
		isolated, readOnly bool
	}{
		{"", "", false, false}, {"false", "true", false, false},
		{"true", "", true, true}, {"true", "false", true, true},
		{"true", "malformed", true, true}, {"malformed", "true", true, true},
		{"true", "true", true, false}, {"1", "1", true, false},
	} {
		t.Run(tc.test+"/"+tc.write, func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", tc.test)
			t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", tc.write)
			if TestInstance() != tc.isolated || ReadOnly() != tc.readOnly || (CheckWritable() != nil) != tc.readOnly {
				t.Fatal("unsafe effective review mode")
			}
		})
	}
}
