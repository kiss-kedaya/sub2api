package service

import "testing"

// Cooling an account down is only a win when another account can take over.
// With a single-account group the cooldown would turn an upstream 502 into a
// "no available accounts" 503, which is worse for the caller.
func TestHasAlternativeAccount(t *testing.T) {
	cases := []struct {
		name       string
		candidates []int64
		accountID  int64
		want       bool
	}{
		{"only a same-account candidate", []int64{29168}, 29168, false},
		{"empty candidate list", nil, 29168, false},
		{"another account present", []int64{29168, 28663}, 29168, true},
		{"other account listed first", []int64{34759, 29168}, 29168, true},
		{"all candidates are other accounts", []int64{28663, 34759}, 29168, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasAlternativeAccount(tc.candidates, tc.accountID); got != tc.want {
				t.Fatalf("hasAlternativeAccount(%v, %d) = %v, want %v", tc.candidates, tc.accountID, got, tc.want)
			}
		})
	}
}

// The 502 path is the one that used to cool an account down unconditionally.
// Retryable-but-account-unrelated failures must still be ignored outright, and
// a nil error must be a no-op rather than a panic.
func TestTempUnscheduleRetryableErrorIgnoresNonRetryable(t *testing.T) {
	svc := &GatewayService{}

	// No error, or an error that is not retryable on the same account: nothing
	// to do, and no repository access happens.
	svc.TempUnscheduleRetryableError(nil, 29168, nil)
	svc.TempUnscheduleRetryableError(nil, 29168, &UpstreamFailoverError{StatusCode: 502})

	// Request-scoped transients never justify touching account scheduling.
	svc.TempUnscheduleRetryableError(nil, 29168, &UpstreamFailoverError{
		StatusCode:             502,
		RetryableOnSameAccount: true,
		RequestScopedTransient: true,
	})
}
