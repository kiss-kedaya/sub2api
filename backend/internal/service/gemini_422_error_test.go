//go:build unit

package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiWriteMappedError_422PreservesGoogleStatusSemantics(t *testing.T) {
	tests := []struct {
		name       string
		googleCode string
		wantStatus int
		wantType   string
	}{
		{"resource exhausted remains rate limited", "RESOURCE_EXHAUSTED", http.StatusTooManyRequests, "rate_limit_error"},
		{"invalid argument remains client error", "INVALID_ARGUMENT", http.StatusBadRequest, "invalid_request_error"},
		{"not found remains missing resource", "NOT_FOUND", http.StatusNotFound, "not_found_error"},
		{"permission denied remains forbidden", "PERMISSION_DENIED", http.StatusForbidden, "permission_error"},
		{"unauthenticated remains unauthorized", "UNAUTHENTICATED", http.StatusUnauthorized, "authentication_error"},
		{"unavailable remains service unavailable", "UNAVAILABLE", http.StatusServiceUnavailable, "overloaded_error"},
		{"internal remains server error", "INTERNAL", http.StatusInternalServerError, "api_error"},
		{"deadline remains gateway timeout", "DEADLINE_EXCEEDED", http.StatusGatewayTimeout, "timeout_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := []byte(`{"error":{"code":422,"message":"upstream response","status":"` + tt.googleCode + `"}}`)

			svc := &GeminiMessagesCompatService{}
			err := svc.writeGeminiMappedError(c, &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey}, http.StatusUnprocessableEntity, "req-422", body)
			require.Error(t, err)
			var payload struct {
				Error struct {
					Type string `json:"type"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, tt.wantType, payload.Error.Type)
		})
	}
}

func TestGeminiWriteMappedError_422ClassifiesUnknownResponses(t *testing.T) {
	tests := []struct {
		name        string
		message     string
		wantStatus  int
		wantType    string
		wantMessage string
	}{
		{"explicit unsupported parameter is a client error", "Unsupported parameter: temperature", http.StatusBadRequest, "invalid_request_error", "Upstream request failed"},
		{"unknown upstream failure is a bad gateway", "backend decoder failed", http.StatusBadGateway, "upstream_error", "Upstream request failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := []byte(`{"error":{"code":422,"message":"` + tt.message + `","status":"UNKNOWN_STATUS"}}`)

			svc := &GeminiMessagesCompatService{}
			err := svc.writeGeminiMappedError(c, &Account{ID: 2, Platform: PlatformGemini, Type: AccountTypeAPIKey}, http.StatusUnprocessableEntity, "req-unknown-422", body)
			require.Error(t, err)
			var payload struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, tt.wantType, payload.Error.Type)
			require.Equal(t, tt.wantMessage, payload.Error.Message)
		})
	}
}

func TestGeminiWriteMappedError_Non422PreservesExistingHTTPStatus(t *testing.T) {
	for _, upstreamStatus := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		for _, tt := range []struct {
			googleStatus string
			wantType     string
		}{
			{"PERMISSION_DENIED", "permission_error"},
			{"UNAUTHENTICATED", "authentication_error"},
			{"UNAVAILABLE", "overloaded_error"},
			{"INTERNAL", "api_error"},
			{"DEADLINE_EXCEEDED", "timeout_error"},
		} {
			t.Run(fmt.Sprintf("%d/%s", upstreamStatus, tt.googleStatus), func(t *testing.T) {
				body, err := json.Marshal(map[string]any{"error": map[string]any{
					"code": upstreamStatus, "status": tt.googleStatus, "message": "upstream response",
				}})
				require.NoError(t, err)
				gin.SetMode(gin.TestMode)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				svc := &GeminiMessagesCompatService{}
				err = svc.writeGeminiMappedError(c, &Account{ID: 3, Platform: PlatformGemini, Type: AccountTypeAPIKey}, upstreamStatus, "req-non-422", body)
				require.Error(t, err)
				var payload struct {
					Error struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"error"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
				require.Equal(t, upstreamStatus, rec.Code)
				require.Equal(t, tt.wantType, payload.Error.Type)
				require.Equal(t, "upstream response", payload.Error.Message)
			})
		}
	}
}
