package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekMediaRequiresDeepSeekGroup(t *testing.T) {
	for _, path := range []string{service.DeepSeekToolsPathASR, service.DeepSeekToolsPathTTS, service.DeepSeekToolsPathOCR} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			c.Set("api_key", &service.APIKey{Group: &service.Group{Platform: service.PlatformGrok}})
			h := &OpenAIGatewayHandler{}
			h.deepSeekTools(c, path, path)
			require.Equal(t, http.StatusNotFound, recorder.Code)
			require.Contains(t, recorder.Body.String(), "only available for DeepSeek groups")
		})
	}
}

func TestDeepSeekMediaBodyValidation(t *testing.T) {
	for _, tc := range []struct {
		name, path, contentType, body string
		tooLarge, invalid             bool
	}{
		{"asr raw", service.DeepSeekToolsPathASR, `multipart/form-data; boundary="native"`, "--native\r\n\xff\x00", false, false},
		{"ocr raw", service.DeepSeekToolsPathOCR, "multipart/form-data; boundary=native", "--native\r\nfile", false, false},
		{"asr json", service.DeepSeekToolsPathASR, "application/json", "{}", false, true},
		{"ocr boundary missing", service.DeepSeekToolsPathOCR, "multipart/form-data", "file", false, true},
		{"empty", service.DeepSeekToolsPathOCR, "multipart/form-data; boundary=native", "", false, true},
		{"tts text", service.DeepSeekToolsPathTTS, "application/json", `{"text":"hello","voice_id":"tide"}`, false, false},
		{"tts empty", service.DeepSeekToolsPathTTS, "application/json", `{"text":" "}`, false, true},
		{"tts duplicate", service.DeepSeekToolsPathTTS, "application/json", `{"text":"hello","text":"unmoderated"}`, false, true},
		{"tts case duplicate", service.DeepSeekToolsPathTTS, "application/json", `{"text":"hello","TEXT":"unmoderated"}`, false, true},
		{"tts missing", service.DeepSeekToolsPathTTS, "application/json", `{}`, false, true},
		{"tts numeric", service.DeepSeekToolsPathTTS, "application/json", `{"text":1}`, false, true},
		{"tts invalid", service.DeepSeekToolsPathTTS, "application/json", `{"text":`, false, true},
		{"tts content type", service.DeepSeekToolsPathTTS, "text/plain", `{"text":"hello"}`, false, true},
		{"tts characters", service.DeepSeekToolsPathTTS, "application/json", `{"text":"` + strings.Repeat("中", 5001) + `"}`, false, true},
		{"tts size", service.DeepSeekToolsPathTTS, "application/json", strings.Repeat(" ", (64<<10)+1), true, true},
		{"asr size", service.DeepSeekToolsPathASR, "multipart/form-data; boundary=native", strings.Repeat("f", (9<<20)+1), true, true},
		{"ocr size", service.DeepSeekToolsPathOCR, "multipart/form-data; boundary=native", strings.Repeat("f", (101<<20)+1), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			body, err := readDeepSeekToolBody(c, tc.path)
			if tc.invalid {
				require.Error(t, err)
				if tc.tooLarge {
					var maxErr *http.MaxBytesError
					require.ErrorAs(t, err, &maxErr)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, []byte(tc.body), body)
			}
		})
	}
}

func TestDeepSeekMediaHandlerValidationStatusCodes(t *testing.T) {
	for _, tc := range []struct {
		path, contentType, body string
		status                  int
	}{
		{service.DeepSeekToolsPathASR, "application/json", "{}", http.StatusBadRequest},
		{service.DeepSeekToolsPathOCR, "application/json", "{}", http.StatusBadRequest},
		{service.DeepSeekToolsPathTTS, "application/json", `{"text":""}`, http.StatusBadRequest},
		{service.DeepSeekToolsPathTTS, "application/json", strings.Repeat(" ", (64<<10)+1), http.StatusRequestEntityTooLarge},
		{service.DeepSeekToolsPathASR, "multipart/form-data; boundary=native", strings.Repeat("f", (9<<20)+1), http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.path+http.StatusText(tc.status), func(t *testing.T) {
			h := newServiceTierHandlerTest(t)
			c, recorder := deepSeekHandlerContext(tc.path, tc.contentType, tc.body)
			h.deepSeekTools(c, tc.path, tc.path)
			require.Equal(t, tc.status, recorder.Code)
		})
	}
}

func deepSeekHandlerContext(path, contentType, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	group := &service.Group{ID: 36, Platform: service.PlatformDeepseek}
	c.Set("api_key", &service.APIKey{ID: 1, Group: group, GroupID: &group.ID, User: &service.User{ID: 2}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 2, Concurrency: 1})
	return c, recorder
}

func TestDeepSeekTTSModeratesOriginalTextBeforeDispatch(t *testing.T) {
	h := newServiceTierHandlerTest(t)
	engine := blockingHandlerPromptEngine()
	h.securityAuditCoordinator = securityaudit.NewCoordinator(nil, engine)
	c, recorder := deepSeekHandlerContext(service.DeepSeekToolsPathTTS, "application/json", `{"text":"spoken content"}`)
	h.DeepSeekToolsTTS(c)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	evaluated, _, requests := engine.snapshot()
	require.Equal(t, 1, evaluated)
	require.Len(t, requests, 1)
	require.Contains(t, string(requests[0].Body), "spoken content")
}

func TestDeepSeekMediaPrereadBodyStillRespectsLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := bytes.Repeat([]byte("f"), (9<<20)+1)
	c.Request = httptest.NewRequest(http.MethodPost, service.DeepSeekToolsPathASR, io.NopCloser(httputil.NewPrereadBody(body)))
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=native")
	_, err := readDeepSeekToolBody(c, service.DeepSeekToolsPathASR)
	var maxErr *http.MaxBytesError
	require.ErrorAs(t, err, &maxErr)
}
