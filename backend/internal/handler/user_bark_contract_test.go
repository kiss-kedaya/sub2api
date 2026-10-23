package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type barkContractEncryptor struct{}

type barkFreshVerifierFunc func(context.Context, int64, string) error

func (f barkFreshVerifierFunc) VerifyFreshCode(ctx context.Context, id int64, code string) error {
	return f(ctx, id, code)
}

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
			h := NewUserBarkHandler(notify.NewUserBarkService(db, barkContractEncryptor{}), nil, nil)
			h.verifier = barkFreshVerifierFunc(func(context.Context, int64, string) error { return nil })
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				c.Next()
			})
			r.PUT("/user/bark", h.UpdateBarkSettings)
			body, want := `{"enabled":false}`, http.StatusOK
			switch test {
			case "toggle existing off":
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT bark_device_key_encrypted,bark_notify_enabled FROM users`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow("", true))
				mock.ExpectExec(`UPDATE users SET bark_device_key_encrypted`).WithArgs("", false, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			case "toggle existing on":
				body = `{"enabled":true}`
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT bark_device_key_encrypted,bark_notify_enabled FROM users`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow("existing-cipher", true))
				mock.ExpectRollback()
			case "clear key":
				body = `{"device_key":"","enabled":false}`
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT bark_device_key_encrypted,bark_notify_enabled FROM users`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow("", false))
				mock.ExpectExec(`UPDATE users SET bark_device_key_encrypted`).WithArgs("", false, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			case "replace key":
				body = `{"device_key":"fixture-new-key","enabled":true}`
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT bark_device_key_encrypted,bark_notify_enabled FROM users`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow("", false))
				mock.ExpectExec(`UPDATE users SET bark_device_key_encrypted`).WithArgs("cipher:fixture-new-key", true, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
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
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT bark_device_key_encrypted,bark_notify_enabled FROM users`).WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"key", "enabled"}).AddRow("", false))
				mock.ExpectRollback()
				want = 400
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("PUT", "https://example.test/user/bark", strings.NewReader(body)))
			require.Equal(t, want, w.Code, w.Body.String())
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.NotContains(t, w.Body.String(), "fixture-new-key")
			require.NotContains(t, w.Body.String(), "cipher:")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBarkChangesRequireFreshTwoFactorBeforeAnyDatabaseWrite(t *testing.T) {
	for _, test := range []string{"missing", "invalid", "replayed", "unavailable", "http"} {
		t.Run(test, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			h := NewUserBarkHandler(notify.NewUserBarkService(db, barkContractEncryptor{}), nil, nil)
			h.verifier = barkFreshVerifierFunc(func(_ context.Context, id int64, code string) error {
				require.Equal(t, int64(1), id)
				switch test {
				case "missing":
					require.Empty(t, code)
					return service.ErrTotpInvalidCode
				case "invalid":
					return service.ErrTotpInvalidCode
				case "replayed":
					return service.ErrTotpCodeReplayed
				default:
					return service.ErrTotpUnavailable
				}
			})
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				c.Next()
			})
			r.PUT("/user/bark", h.UpdateBarkSettings)
			url := "https://example.test/user/bark"
			if test == "http" {
				url = "http://example.test/user/bark"
			}
			req := httptest.NewRequest("PUT", url, strings.NewReader(`{"device_key":"fixture-new-key","enabled":true}`))
			if test != "missing" {
				req.Header.Set("X-Bark-TOTP", "123456")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.GreaterOrEqual(t, w.Code, 400, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
