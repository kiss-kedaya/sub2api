package admin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountHandlerSchedulingFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, field := range []string{"priority", "concurrency"} {
		for _, value := range []struct {
			name string
			json string
			want int
		}{
			{name: "zero", json: "0", want: http.StatusOK},
			{name: "positive", json: "7", want: http.StatusOK},
			{name: "negative", json: "-1", want: http.StatusBadRequest},
			{name: "fractional", json: "0.5", want: http.StatusBadRequest},
			{name: "string", json: `"0"`, want: http.StatusBadRequest},
			{name: "overflow", json: "9223372036854775808", want: http.StatusBadRequest},
		} {
			for _, operation := range []string{"create", "update", "batch-create", "bulk-update"} {
				t.Run(field+"/"+value.name+"/"+operation, func(t *testing.T) {
					svc := newStubAdminService()
					h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
					r := gin.New()
					method, path := http.MethodPost, "/accounts"
					body := fmt.Sprintf(`{"name":"test","platform":"anthropic","type":"apikey","credentials":{"api_key":"test"},%q:%s}`, field, value.json)
					switch operation {
					case "create":
						r.POST(path, h.Create)
					case "update":
						method, path = http.MethodPut, "/accounts/1"
						r.PUT("/accounts/:id", h.Update)
					case "batch-create":
						path = "/accounts/batch"
						r.POST(path, h.BatchCreate)
						body = `{"accounts":[` + body + `]}`
					case "bulk-update":
						path = "/accounts/bulk-update"
						r.POST(path, h.BulkUpdate)
						body = fmt.Sprintf(`{"account_ids":[1],%q:%s}`, field, value.json)
					}
					recorder := httptest.NewRecorder()
					request := httptest.NewRequest(method, path, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					r.ServeHTTP(recorder, request)
					require.Equal(t, value.want, recorder.Code, recorder.Body.String())
					if value.want == http.StatusBadRequest {
						require.Empty(t, svc.createdAccounts)
						require.Zero(t, svc.updateAccountCalls)
						require.Nil(t, svc.lastBulkUpdateAccountInput)
					}
				})
			}
		}
	}
}

func TestAccountHandlerBatchCreateInvalidPriorityDoesNotPartiallyWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/accounts/batch", h.BatchCreate)
	body := `{"accounts":[{"name":"valid","platform":"anthropic","type":"apikey","credentials":{"api_key":"test"},"priority":0},{"name":"invalid","platform":"anthropic","type":"apikey","credentials":{"api_key":"test"},"priority":-1}]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/accounts/batch", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Empty(t, svc.createdAccounts)
}
