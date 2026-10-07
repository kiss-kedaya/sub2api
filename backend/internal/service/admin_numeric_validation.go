package service

import (
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func validateUserSchedulingLimits(concurrency, rpmLimit *int) error {
	if concurrency != nil && *concurrency < 0 {
		return infraerrors.BadRequest("INVALID_USER_CONCURRENCY", "concurrency must be >= 0")
	}
	if rpmLimit != nil && *rpmLimit < 0 {
		return infraerrors.BadRequest("INVALID_USER_RPM_LIMIT", "rpm_limit must be >= 0")
	}
	return nil
}

func validateGroupLimitValues(limits ...*float64) error {
	for _, limit := range limits {
		if limit != nil && (math.IsNaN(*limit) || math.IsInf(*limit, 0)) {
			return infraerrors.BadRequest("INVALID_GROUP_LIMIT", "group limits must be finite")
		}
	}
	return nil
}
