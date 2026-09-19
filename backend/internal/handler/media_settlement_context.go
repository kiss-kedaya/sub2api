package handler

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
)

var errMediaSettlementNotRecorded = errors.New("media settlement was not recorded")

const mediaSettlementContextKey = "handler.media_settlement"

type mediaSettlementState struct {
	recorded bool
	err      error
}

func requireMediaSettlement(c *gin.Context) {
	if c != nil {
		c.Set(mediaSettlementContextKey, &mediaSettlementState{})
	}
}

func mediaSettlementRequired(c *gin.Context) bool {
	_, ok := mediaSettlementStateFromContext(c)
	return ok
}

func recordMediaSettlementResult(c *gin.Context, err error) {
	if state, ok := mediaSettlementStateFromContext(c); ok {
		state.recorded = true
		state.err = err
	}
}

func mediaSettlementResult(c *gin.Context) error {
	state, ok := mediaSettlementStateFromContext(c)
	if !ok || !state.recorded {
		return errMediaSettlementNotRecorded
	}
	return state.err
}

func dispatchMediaSettlement(c *gin.Context, settle func(context.Context) error, enqueue func()) {
	if !mediaSettlementRequired(c) {
		if enqueue != nil {
			enqueue()
		}
		return
	}
	if settle == nil {
		recordMediaSettlementResult(c, errMediaSettlementNotRecorded)
		return
	}
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	recordMediaSettlementResult(c, settle(ctx))
}

func mediaSettlementStateFromContext(c *gin.Context) (*mediaSettlementState, bool) {
	if c == nil {
		return nil, false
	}
	value, ok := c.Get(mediaSettlementContextKey)
	if !ok {
		return nil, false
	}
	state, ok := value.(*mediaSettlementState)
	return state, ok && state != nil
}
