package wallet

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
)

func operatorAuditActor() AuditActor {
	userID := int64(9001)
	return AuditActor{
		UserID:     &userID,
		Email:      "ops-admin@example.com",
		ClientIP:   "203.0.113.7",
		UserAgent:  "unit-agent/1.0",
		RequestID:  "req-20261121-1",
		AuthMethod: "jwt",
	}
}

func operatorAuditArgs() []driver.Value {
	actor := operatorAuditActor()
	return []driver.Value{*actor.UserID, actor.Email, actor.ClientIP, actor.UserAgent, actor.RequestID, actor.AuthMethod}
}

// The two high-risk export paths must persist the operator and request
// provenance on the same row as the operation they authorized.
func TestExportAuditsOperatorAndRequestProvenance(t *testing.T) {
	actor := operatorAuditActor()

	t.Run("mnemonic export", func(t *testing.T) {
		m, mock := mockManager(t)
		expectExport(mock, managedVault(t))
		args := append([]driver.Value{"mnemonic_export", "", 0, 0, "", "", "", "success", ""}, operatorAuditArgs()...)
		mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs(args...).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		if _, err := m.ExportMnemonics(WithAuditActor(t.Context(), actor)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("private key export", func(t *testing.T) {
		m, mock := mockManager(t)
		raw := managedVault(t)
		address := exportTestAddress(t, testDeposit, addresses.FamilyEVM, 0)
		expectAddressExportStart(mock, raw)
		expectExportAssignment(mock, "evm", address, "active", 0)
		expectExportPool(mock, "evm", address, "assigned", 0)
		args := append([]driver.Value{addressPrivateKeyExportOp, "evm", 0, 0, address, "", "", "success", ""}, operatorAuditArgs()...)
		mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs(args...).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		if _, err := m.ExportAddressPrivateKey(WithAuditActor(t.Context(), actor), exportTestAddressID); err != nil {
			t.Fatal(err)
		}
	})
}

// No operator context must still audit and export successfully; the six
// attribution columns are written as NULL, never as empty strings.
func TestExportWithoutOperatorContextWritesNulls(t *testing.T) {
	m, mock := mockManager(t)
	expectExport(mock, managedVault(t))
	mock.ExpectExec(regexp.QuoteMeta(auditSQL)).
		WithArgs("mnemonic_export", "", 0, 0, "", "", "", "success", "", nil, nil, nil, nil, nil, nil).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if _, err := m.ExportMnemonics(context.Background()); err != nil {
		t.Fatal(err)
	}
	if actor := AuditActorFromContext(context.Background()); actor.UserID != nil || actor != (AuditActor{}) {
		t.Fatal("unattributed context must resolve to the zero actor")
	}
}

// A refused export is still a high-risk attempt and must name the operator:
// the denial audit runs on a detached context, so attribution has to survive
// the detach explicitly.
func TestDeniedExportAuditsOperator(t *testing.T) {
	m, mock := mockManager(t)
	mock.ExpectBegin()
	expectRead(mock, "corrupted", " FOR SHARE")
	mock.ExpectRollback()
	args := append([]driver.Value{"mnemonic_export", "", 0, 0, "", "", "", "denied", ""}, operatorAuditArgs()...)
	mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs(args...).WillReturnResult(sqlmock.NewResult(1, 1))
	if value, err := m.ExportMnemonics(WithAuditActor(t.Context(), operatorAuditActor())); value != nil || err == nil {
		t.Fatal("corrupt vault must not export")
	}
}

func TestAuditActorNormalization(t *testing.T) {
	zero, negative := int64(0), int64(-4)
	for name, actor := range map[string]AuditActor{
		"zero user":     {UserID: &zero},
		"negative user": {UserID: &negative},
	} {
		t.Run(name, func(t *testing.T) {
			if got := AuditActorFromContext(WithAuditActor(context.Background(), actor)); got.UserID != nil {
				t.Fatal("non-positive operator id must be dropped")
			}
		})
	}

	truncated := AuditActorFromContext(WithAuditActor(context.Background(), AuditActor{
		Email:      "  " + strings.Repeat("e", 300) + "@example.com  ",
		ClientIP:   " 203.0.113.9 ",
		UserAgent:  strings.Repeat("u", 900),
		RequestID:  strings.Repeat("r", 300),
		AuthMethod: strings.Repeat("a", 300),
	}))
	if truncated.Email != strings.Repeat("e", maxAuditActorEmail) {
		t.Fatalf("email not bounded: %d bytes", len(truncated.Email))
	}
	if truncated.ClientIP != "203.0.113.9" {
		t.Fatalf("client IP not trimmed: %q", truncated.ClientIP)
	}
	if len(truncated.UserAgent) != maxAuditUserAgent || len(truncated.RequestID) != maxAuditRequestID ||
		len(truncated.AuthMethod) != maxAuditAuthMethod {
		t.Fatalf("metadata not bounded: %d/%d/%d", len(truncated.UserAgent), len(truncated.RequestID), len(truncated.AuthMethod))
	}

	// Attribution is optional: a nil context must not panic in either helper.
	if actor := AuditActorFromContext(nil); actor != (AuditActor{}) {
		t.Fatal("nil context must resolve to the zero actor")
	}
	if WithAuditActor(nil, AuditActor{Email: "ops@example.com"}) == nil {
		t.Fatal("nil context must be upgraded to a context")
	}
	if actor := AuditActorFromContext(WithAuditActor(nil, AuditActor{Email: "ops@example.com"})); actor.Email != "ops@example.com" {
		t.Fatal("attribution lost on nil context")
	}
}
