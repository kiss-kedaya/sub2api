//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// mismatchGroupRepo serves a different group per id so a smart-routing key
// (several candidate groups) can be modelled; an unknown id is "not found".
type mismatchGroupRepo struct {
	fakeGroupRepo
	groups map[int64]*service.Group
}

func (r *mismatchGroupRepo) GetByID(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByIDLite(ctx, id)
}

func (r *mismatchGroupRepo) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if group, ok := r.groups[id]; ok {
		return group, nil
	}
	return nil, service.ErrGroupNotFound
}

func mismatchTestGroup(id int64, platform string, conversion bool) *service.Group {
	return &service.Group{ID: id, Hydrated: true, Platform: platform, Status: service.StatusActive, CrossProtocolConversionEnabled: conversion}
}

// mismatchTestHandler wires a real GatewayService whose group lookup knows only
// the given groups.
func mismatchTestHandler(known ...*service.Group) *GatewayHandler {
	byID := make(map[int64]*service.Group, len(known))
	for _, group := range known {
		byID[group.ID] = group
	}
	return &GatewayHandler{gatewayService: service.NewGatewayService(
		nil, &mismatchGroupRepo{groups: byID}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)}
}

// mismatchTestKey is a key whose primary group is the first one; more than one
// group makes it a smart-routing key (RouteGroupIDs in the owner's order).
func mismatchTestKey(groups ...*service.Group) *service.APIKey {
	primaryID := groups[0].ID
	key := &service.APIKey{ID: 71, UserID: 72, GroupID: &primaryID, Group: groups[0], User: &service.User{ID: 72}}
	if len(groups) > 1 {
		for _, group := range groups {
			key.RouteGroupIDs = append(key.RouteGroupIDs, group.ID)
		}
	}
	return key
}

func mismatchTestContext(path string, apiKey *service.APIKey, resolvedPlatform string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	if resolvedPlatform != "" {
		c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), resolvedPlatform))
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 1})
	return c, recorder
}

func TestProtocolMismatchApplies_KeyRoutes(t *testing.T) {
	offA := mismatchTestGroup(11, service.PlatformAnthropic, false)
	offB := mismatchTestGroup(12, service.PlatformAnthropic, false)
	onA := mismatchTestGroup(13, service.PlatformAnthropic, true)
	offOpenAI := mismatchTestGroup(14, service.PlatformOpenAI, false)

	tests := []struct {
		name     string
		handler  *GatewayHandler
		key      *service.APIKey
		resolved string
		want     bool
	}{
		{
			name:    "single group with conversion off is refused",
			handler: mismatchTestHandler(offA), key: mismatchTestKey(offA), want: true,
		},
		{
			// The primary group decides alone: even a lookup that cannot read the group
			// must not let the request through (behaviour unchanged for plain keys).
			name:    "single group is decided by its own flag even when the lookup knows nothing",
			handler: mismatchTestHandler(), key: mismatchTestKey(offA), want: true,
		},
		{
			name:    "single group without a gateway service is still refused",
			handler: &GatewayHandler{}, key: mismatchTestKey(offA), want: true,
		},
		{
			name:    "single group with conversion on passes",
			handler: mismatchTestHandler(onA), key: mismatchTestKey(onA), want: false,
		},
		{
			name:    "key without a group passes",
			handler: mismatchTestHandler(), key: &service.APIKey{ID: 1, UserID: 2, User: &service.User{ID: 2}}, want: false,
		},
		{
			name:    "smart route: primary off, a later anthropic group on, is not refused at ingress",
			handler: mismatchTestHandler(offA, onA), key: mismatchTestKey(offA, onA), resolved: service.PlatformAnthropic, want: false,
		},
		{
			name:    "smart route: the enabled group may sit anywhere after the primary",
			handler: mismatchTestHandler(offA, offB, onA), key: mismatchTestKey(offA, offB, onA), resolved: service.PlatformAnthropic, want: false,
		},
		{
			name:    "smart route: every candidate off is refused",
			handler: mismatchTestHandler(offA, offB), key: mismatchTestKey(offA, offB), resolved: service.PlatformAnthropic, want: true,
		},
		{
			name:    "smart route: primary on passes as before",
			handler: mismatchTestHandler(onA, offA), key: mismatchTestKey(onA, offA), resolved: service.PlatformAnthropic, want: false,
		},
		{
			// Missing data never becomes a non-retryable 400.
			name:    "smart route: an unreadable later group counts as maybe-allowed",
			handler: mismatchTestHandler(offA), key: mismatchTestKey(offA, mismatchTestGroup(99, service.PlatformAnthropic, false)), resolved: service.PlatformAnthropic, want: false,
		},
		{
			name:    "an OpenAI-platform group is never refused by this guard",
			handler: mismatchTestHandler(offOpenAI), key: mismatchTestKey(offOpenAI), want: false,
		},
		{
			name:    "smart route: the guard stays limited to an anthropic-resolved request",
			handler: mismatchTestHandler(offA, offOpenAI), key: mismatchTestKey(offA, offOpenAI), resolved: service.PlatformOpenAI, want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := mismatchTestContext("/v1/responses", tt.key, tt.resolved)
			require.Equal(t, tt.want, tt.handler.protocolMismatchApplies(c, tt.key))
		})
	}
}

// The ingress rejection keeps its status and body shape; a key that still has a
// conversion-enabled group moves on to the next step (body parsing, then account
// selection) instead of being refused.
func TestProtocolMismatchGuard_IngressResponses(t *testing.T) {
	offA := mismatchTestGroup(21, service.PlatformAnthropic, false)
	offB := mismatchTestGroup(22, service.PlatformAnthropic, false)
	onA := mismatchTestGroup(23, service.PlatformAnthropic, true)

	endpoints := []struct {
		name string
		path string
		call func(*GatewayHandler, *gin.Context)
	}{
		{name: "responses", path: "/v1/responses", call: (*GatewayHandler).Responses},
		{name: "chat completions", path: "/v1/chat/completions", call: (*GatewayHandler).ChatCompletions},
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint.name+"/single group off is refused", func(t *testing.T) {
			key := mismatchTestKey(offA)
			c, recorder := mismatchTestContext(endpoint.path, key, "")
			endpoint.call(mismatchTestHandler(offA), c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "PROTOCOL_MISMATCH", gjson.Get(recorder.Body.String(), "error.code").String())
			require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
			require.Equal(t, endpoint.path, gjson.Get(recorder.Body.String(), "error.path").String())
			require.Equal(t, service.PlatformAnthropic, gjson.Get(recorder.Body.String(), "error.group_platform").String())
			require.NotEmpty(t, gjson.Get(recorder.Body.String(), "error.hint").String())
		})
		t.Run(endpoint.name+"/smart route with every group off is refused", func(t *testing.T) {
			key := mismatchTestKey(offA, offB)
			c, recorder := mismatchTestContext(endpoint.path, key, service.PlatformAnthropic)
			endpoint.call(mismatchTestHandler(offA, offB), c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "PROTOCOL_MISMATCH", gjson.Get(recorder.Body.String(), "error.code").String())
			require.Equal(t, service.PlatformAnthropic, gjson.Get(recorder.Body.String(), "error.group_platform").String())
		})
		t.Run(endpoint.name+"/smart route with a later group on is not refused at ingress", func(t *testing.T) {
			key := mismatchTestKey(offA, onA)
			c, recorder := mismatchTestContext(endpoint.path, key, service.PlatformAnthropic)
			endpoint.call(mismatchTestHandler(offA, onA), c)

			// The empty body is rejected by the very next step, which proves the
			// request got past the protocol guard.
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.NotEqual(t, "PROTOCOL_MISMATCH", gjson.Get(recorder.Body.String(), "error.code").String())
			require.Equal(t, "Request body is empty", gjson.Get(recorder.Body.String(), "error.message").String())
		})
	}
}
