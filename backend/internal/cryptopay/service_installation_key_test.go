package cryptopay

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestWalletUsesPersistentInstallationKeyWithoutDedicatedPaymentKey(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(map[bool]string{false: "process-local", true: "persisted"}[persistent], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			settings := service.NewSettingService(readOnlyConfigRepository{raw: "{}"}, nil)
			appConfig := &config.Config{Totp: config.TotpConfig{
				EncryptionKey: strings.Repeat("ab", 32), EncryptionKeyPersistent: persistent,
			}}
			s := NewService(db, settings, nil, nil, appConfig)
			mock.ExpectQuery("SELECT value FROM settings").WithArgs("crypto.signer.vault").
				WillReturnRows(sqlmock.NewRows([]string{"value"}))
			status, err := s.WalletStatus(t.Context())
			if err != nil || status.OK != persistent {
				t.Fatalf("wallet encryption ready = %v, want %v, err=%v", status, persistent, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
