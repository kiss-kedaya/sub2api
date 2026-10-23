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

// Only the primary administrator can subscribe to private operational data.
func TestSendToAdminsSelectsOnlyPrimaryAdminKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT bark_device_key_encrypted FROM users`).
		WithArgs(int64(1), "admin").
		WillReturnRows(sqlmock.NewRows([]string{"bark_device_key_encrypted"}))

	svc := NewUserBarkService(db, passthroughEncryptor{})
	if err := svc.SendToAdmins(context.Background(), bark.Notification{Title: "t"}); err != nil {
		t.Fatalf("empty admin list must be a no-op, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSendToAdminsQueryExcludesInactiveAndDeletedUsers(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`(?s)SELECT bark_device_key_encrypted FROM users.*id = \$1 AND role = \$2 AND bark_notify_enabled = TRUE.*status = 'active' AND deleted_at IS NULL.*bark_device_key_encrypted <> ''`).
		WithArgs(int64(1), "admin").
		WillReturnRows(sqlmock.NewRows([]string{"bark_device_key_encrypted"}))
	if err := NewUserBarkService(db, passthroughEncryptor{}).SendToAdmins(context.Background(), bark.Notification{}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// An unreadable owner key must surface an error without another recipient.
func TestSendToAdminsReportsOwnerDecryptFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`SELECT bark_device_key_encrypted FROM users`).
		WithArgs(int64(1), "admin").
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
