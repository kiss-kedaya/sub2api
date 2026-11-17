package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekChatBodyTooLarge(t *testing.T) {
	deepSeekKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformDeepseek}}
	openAIKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
	over, at := deepSeekChatBodyLimit+1, deepSeekChatBodyLimit
	for _, tc := range []struct {
		name  string
		key   *service.APIKey
		model string
		size  int64
		want  bool
	}{
		{"deepseek group over the limit, model not parsed yet", deepSeekKey, "", over, true},
		{"deepseek group exactly at the limit", deepSeekKey, "deepseek-v4.1-flash", at, false},
		{"deepseek group, unknown content length", deepSeekKey, "", -1, false},
		{"smart route entry group, deepseek model", openAIKey, "deepseek-v4.1-flash", over, true},
		{"model prefix is case insensitive", openAIKey, " DeepSeek-Flash", over, true},
		{"other model keeps the global limit", openAIKey, "gpt-test", over, false},
		{"other platform before the model is known", openAIKey, "", over, false},
		{"no key, deepseek model", nil, "deepseek-flash", over, true},
		{"no key, no model", nil, "", over, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, deepSeekChatBodyTooLarge(tc.key, tc.model, tc.size))
		})
	}
}

// failOnRead proves a request was rejected from its declared length alone.
type failOnRead struct{ t *testing.T }

func (r failOnRead) Read([]byte) (int, error) {
	r.t.Error("oversized DeepSeek request body was read")
	return 0, errors.New("must not be read")
}

func TestDeepSeekChatBodyCapRejectsBeforeReading(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(7)
	deepSeekGroup := &service.Group{ID: groupID, Platform: service.PlatformDeepseek, AllowMessagesDispatch: true}
	for _, tc := range []struct {
		name string
		path string
		call func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{"responses", "/v1/responses", (*OpenAIGatewayHandler).Responses},
		{"chat completions", "/v1/chat/completions", (*OpenAIGatewayHandler).ChatCompletions},
		{"messages", "/v1/messages", (*OpenAIGatewayHandler).Messages},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, io.NopCloser(failOnRead{t}))
			c.Request.ContentLength = deepSeekChatBodyLimit + 1
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 10, GroupID: &groupID, Group: deepSeekGroup})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1, Concurrency: 1})

			tc.call(newOpenAIHandlerForPreviousResponseIDValidation(t, nil), c)

			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
			require.Contains(t, rec.Body.String(), "invalid_request_error")
			require.Contains(t, rec.Body.String(), "Request body too large, limit is 20MB")
		})
	}
}

func TestDeepSeekChatBodyCapUsesRealSizeAndModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(8)
	// A smart-route key enters through another platform; only the parsed model
	// reveals that the request is headed for DeepSeek. No Content-Length is sent.
	entryGroup := &service.Group{ID: groupID, Platform: service.PlatformOpenAI}
	padding := strings.Repeat("a", int(deepSeekChatBodyLimit))
	for _, tc := range []struct {
		name     string
		model    string
		rejected bool
	}{
		{"deepseek model", "deepseek-v4.1-flash", true},
		{"other model", "gpt-test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"` + tc.model + `","input":"` + padding + `"}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.ContentLength = -1
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 11, GroupID: &groupID, Group: entryGroup})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1, Concurrency: 1})

			h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
			// The bare test service cannot schedule; a request that passes the cap
			// fails later for an unrelated reason, never with the 20MB message.
			func() {
				defer func() { _ = recover() }()
				h.Responses(c)
			}()

			tooLarge := rec.Code == http.StatusRequestEntityTooLarge &&
				strings.Contains(rec.Body.String(), "Request body too large, limit is 20MB")
			require.Equal(t, tc.rejected, tooLarge, "status=%d body=%.200s", rec.Code, rec.Body.String())
		})
	}
}
