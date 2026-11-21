package service

import "github.com/gin-gonic/gin"

// openAICompatStreamEnd settles a converted stream (Chat Completions or Messages
// served by an OpenAI-family upstream) that ended without a terminal frame.
//
// A stream that ended before any client-visible output is replayable: the caller
// passes replayable=true so the attempt is returned as a failover error the handler
// may retry on another account. The estimate is still applied, and because the error
// is marked StreamEndedWithoutTerminal the handler settles it only when no retry
// succeeds, so the upstream cost is not lost for free while the logical request is
// still billed at most once (issue #456).
//
// After output reached the client, or when the client is gone, the request is
// settled locally from the estimate and no failover error is produced.
func (s *OpenAIGatewayService) openAICompatStreamEnd(
	c *gin.Context,
	account *Account,
	requestID, upstreamModel, message string,
	replayable bool,
	cause error,
	finish func(error) (*OpenAIForwardResult, error),
) (*OpenAIForwardResult, error) {
	if replayable {
		cause = markOpenAIStreamEndedWithoutTerminal(
			s.newOpenAIStreamFailoverErrorWithModel(c, account, false, requestID, nil, message, upstreamModel),
		)
	}
	return finish(cause)
}
