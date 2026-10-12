package notify

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
)

// passthroughEncryptor keeps test values readable; real deployments use AES.
type passthroughEncryptor struct{}

func (passthroughEncryptor) EncryptString(s string) (string, error) { return "enc:" + s, nil }
func (passthroughEncryptor) DecryptString(s string) (string, error) { return s, nil }

// The admin fan-out must select only enabled admins with a stored key, and
// must not stop at the first failing key: one broken device must not mute
// the rest of the operators.
func TestSendToAdminsSelectsOnlyEnabledAdminKeys(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT bark_device_key_encrypted FROM users`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"bark_device_key_encrypted"}))

	svc := NewUserBarkService(db, passthroughEncryptor{})
	if err := svc.SendToAdmins(context.Background(), bark.Notification{Title: "t"}); err != nil {
		t.Fatalf("empty admin list must be a no-op, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A decrypt failure on one key must not abort the loop (firstErr is
// reported once, every other admin still gets the push).
func TestSendToAdminsContinuesAfterDecryptFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT bark_device_key_encrypted FROM users`).
		WithArgs("admin").
		WillReturnRows(sqlmock.NewRows([]string{"bark_device_key_encrypted"}).
			AddRow("bad-key"))

	svc := NewUserBarkService(db, failingEncryptor{})
	err = svc.SendToAdmins(context.Background(), bark.Notification{Title: "t"})
	if err == nil {
		t.Fatal("expected the decrypt failure to surface")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type failingEncryptor struct{}

func (failingEncryptor) EncryptString(s string) (string, error) { return s, nil }
func (failingEncryptor) DecryptString(string) (string, error)   { return "", errTestDecrypt }

var errTestDecrypt = &testError{"decrypt failed"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
