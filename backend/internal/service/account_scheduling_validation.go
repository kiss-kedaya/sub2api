package service

import infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

// ValidateAccountSchedulingFields also protects non-HTTP account writers.
// Nil leaves a field unchanged; zero is an explicit, valid value.
func ValidateAccountSchedulingFields(concurrency, priority *int) error {
	if concurrency != nil && *concurrency < 0 {
		return infraerrors.BadRequest("INVALID_ACCOUNT_CONCURRENCY", "concurrency must be >= 0")
	}
	if priority != nil && *priority < 0 {
		return infraerrors.BadRequest("INVALID_ACCOUNT_PRIORITY", "priority must be >= 0")
	}
	return nil
}
