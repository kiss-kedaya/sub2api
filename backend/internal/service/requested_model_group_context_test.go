package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestContextShouldReturnRequestedModelUsesSelectedGroup(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "selected_disabled", true: "selected_enabled"}[enabled], func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			c.Set("api_key", &APIKey{Group: &Group{ID: 1, ReturnRequestedModel: !enabled}})
			selected := &Group{ID: 2, ReturnRequestedModel: enabled}
			key := &APIKey{GroupID: &selected.ID, Group: selected}
			c.Request = c.Request.WithContext(ContextWithAPIKeyRoute(c.Request.Context(), key))
			require.Equal(t, enabled, ContextShouldReturnRequestedModel(c))
		})
	}
}

func TestContextShouldReturnRequestedModelFallbackAndMissing(t *testing.T) {
	require.False(t, ContextShouldReturnRequestedModel(nil))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, ContextShouldReturnRequestedModel(c))
	c.Set("api_key", (*APIKey)(nil))
	require.False(t, ContextShouldReturnRequestedModel(c))
	c.Set("api_key", &APIKey{Group: &Group{ReturnRequestedModel: true}})
	require.True(t, ContextShouldReturnRequestedModel(c))
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(
		context.WithValue(context.Background(), ctxkey.Group, (*Group)(nil)))
	require.True(t, ContextShouldReturnRequestedModel(c))
}
