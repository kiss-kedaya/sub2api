package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type barkContractEncryptor struct{}

func (barkContractEncryptor) EncryptString(v string) (string, error) { return "cipher:" + v, nil }
func (barkContractEncryptor) DecryptString(string) (string, error) {
	panic("settings must never decrypt or return the key")
}

func TestBarkSettingsPreserveOmittedKeyAndOnlyClearExplicitly(t *testing.T) {
	for _, test := range []string{"toggle existing off", "toggle existing on", "clear key", "replace key", "missing toggle", "unknown field", "trailing payload", "invalid key", "enable without key"} {
		t.Run(test, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			h := NewUserBarkHandler(notify.NewUserBarkService(db, barkContractEncryptor{}))
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				c.Next()
			})
			r.PUT("/user/bark", h.UpdateBarkSettings)
			body, want := `{"enabled":false}`, http.StatusOK
			switch test {
			case "toggle existing off":
				mock.ExpectExec(`UPDATE users SET bark_notify_enabled`).WithArgs(false, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			case "toggle existing on":
				body = `{"enabled":true}`
				mock.ExpectExec(`UPDATE users SET bark_notify_enabled`).WithArgs(true, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			case "clear key":
				body = `{"device_key":"","enabled":false}`
				mock.ExpectExec(`UPDATE users SET bark_device_key_encrypted`).WithArgs("", false, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			case "replace key":
				body = `{"device_key":"fixture-new-key","enabled":true}`
				mock.ExpectExec(`UPDATE users SET bark_device_key_encrypted`).WithArgs("cipher:fixture-new-key", true, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			case "missing toggle":
				body = `{"device_key":"fixture-new-key"}`
				want = 400
			case "unknown field":
				body = `{"enabled":true,"user_id":2}`
				want = 400
			case "trailing payload":
				body = `{"enabled":true}{}`
				want = 400
			case "invalid key":
				body = `{"enabled":true,"device_key":"https://example.test/private-key"}`
				want = 400
			case "enable without key":
				body = `{"enabled":true}`
				mock.ExpectExec(`UPDATE users SET bark_notify_enabled`).WithArgs(true, int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
				want = 400
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("PUT", "/user/bark", strings.NewReader(body)))
			require.Equal(t, want, w.Code, w.Body.String())
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotContains(t, w.Body.String(), "fixture-new-key")
			require.NotContains(t, w.Body.String(), "cipher:")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
