package handler

import (
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// A stopped generation still needs its zero-cost usage row and hold finalization,
// but its request-scoped silence is not a scheduler-health failure.
func openAIWSTurnSilenceFailure(result *service.OpenAIForwardResult, err error) bool {
	var waitLimit *service.UpstreamFailoverError
	return result != nil && result.NonBillableUpstreamError && errors.As(err, &waitLimit) && waitLimit.IsUpstreamWaitLimit()
}
