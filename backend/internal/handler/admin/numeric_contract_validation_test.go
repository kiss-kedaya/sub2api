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

func TestAdminUserNumericContractRejectsNegativeLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, field := range []string{"concurrency", "rpm_limit"} {
		for _, value := range []int{-1, 0, 1} {
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				t.Run(fmt.Sprintf("%s/%d/%s", field, value, method), func(t *testing.T) {
					svc := newStubAdminService()
					h := NewUserHandler(svc, nil, nil, nil, nil, nil, nil)
					r := gin.New()
					r.POST("/users", h.Create)
					r.PUT("/users/:id", h.Update)
					path := "/users"
					if method == http.MethodPut {
						path += "/1"
					}
					body := fmt.Sprintf(`{"email":"test@example.test","password":"fixture-password",%q:%d}`, field, value)
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(method, path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					r.ServeHTTP(rec, req)
					want := http.StatusOK
					if value < 0 {
						want = http.StatusBadRequest
					}
					require.Equal(t, want, rec.Code, rec.Body.String())
				})
			}
		}
	}
}

func TestAdminGroupNumericContractRejectsNonFiniteLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, field := range []string{"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd"} {
		for _, value := range []string{`"NaN"`, `"+Inf"`, `"-Inf"`, `"Infinity"`, `"1e309"`, `0`, `null`, `-1`, `"42.5"`} {
			for _, method := range []string{http.MethodPost, http.MethodPut} {
				t.Run(field+"/"+value+"/"+method, func(t *testing.T) {
					svc := newStubAdminService()
					h := NewGroupHandler(svc, nil, nil)
					r := gin.New()
					r.POST("/groups", h.Create)
					r.PUT("/groups/:id", h.Update)
					path := "/groups"
					if method == http.MethodPut {
						path += "/1"
					}
					body := fmt.Sprintf(`{"name":"fixture","platform":"anthropic","rate_multiplier":1,%q:%s}`, field, value)
					rec := httptest.NewRecorder()
					req := httptest.NewRequest(method, path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					r.ServeHTTP(rec, req)
					valid := value == `0` || value == `null` || value == `-1` || value == `"42.5"`
					if valid {
						require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
					} else {
						require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
						require.Empty(t, svc.createdGroups)
						require.Empty(t, svc.updatedGroups)
					}
				})
			}
		}
	}
}
