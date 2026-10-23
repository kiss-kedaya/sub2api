package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func newGrantContext(t *testing.T, m *Manager) (context.Context, func()) {
	t.Helper()
	m.securityStatus = securityView(emptySecurityState())
	ctx, revoke, err := m.AuthorizeSweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revoke)
	return ctx, revoke
}

func TestSigningAuthorityIsIndependentAndContextScoped(t *testing.T) {
	a, _, _ := guardedManager(t)
	b, _, _ := guardedManager(t)
	ctx, revoke := newGrantContext(t, a)
	if !a.validSigningGrant(ctx) || b.validSigningGrant(ctx) || a.validSigningGrant(t.Context()) {
		t.Fatal("signing capability escaped its operation/process")
	}
	if string(a.signingAuthority) == string(a.securityKey) || string(a.signingAuthority) == string(b.signingAuthority) {
		t.Fatal("signing authority is not independently random")
	}
	revoke()
	revoke()
	if a.validSigningGrant(ctx) {
		t.Fatal("completed sweep retained signing authority")
	}
}

func TestSigningGrantsExpireAndCannotBeForgedOrRetargeted(t *testing.T) {
	m, _, _ := guardedManager(t)
	ctx, _ := newGrantContext(t, m)
	grant, ok := ctx.Value(signingGrantContextKey{}).(*signingGrant)
	if !ok {
		t.Fatal("missing signing grant fixture")
	}
	forged := *grant
	forged.expires++
	if m.validSigningGrant(context.WithValue(ctx, signingGrantContextKey{}, &forged)) {
		t.Fatal("changed capability claims accepted")
	}
	expired := *grant
	expired.expires = time.Now().Add(-time.Second).UnixNano()
	expired.mac = m.grantMAC(&expired)
	if m.validSigningGrant(context.WithValue(ctx, signingGrantContextKey{}, &expired)) {
		t.Fatal("expired capability accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if m.validSigningGrant(canceled) {
		t.Fatal("canceled operation retained signing authority")
	}
	if err := m.SetDestination("0x3333333333333333333333333333333333333333"); err != nil {
		t.Fatal(err)
	}
	if m.validSigningGrant(ctx) {
		t.Fatal("changed destination inherited a prior capability")
	}
}

func TestDisableCleanupAndIncidentRevokeAllSigningGrants(t *testing.T) {
	for _, action := range []string{"disable", "close", "incident"} {
		t.Run(action, func(t *testing.T) {
			m, _, _ := guardedManager(t)
			ctx, _ := newGrantContext(t, m)
			switch action {
			case "disable":
				m.SetEnabled(false)
				m.SetEnabled(true)
			case "close":
				m.Close()
			case "incident":
				m.localIncident("unauthorized_token_outflow", "bsc", "0x01")
			}
			if m.validSigningGrant(ctx) {
				t.Fatal("revoked signing capability revived")
			}
			if action == "incident" {
				if _, _, err := m.AuthorizeSweep(t.Context()); !errors.Is(err, ErrSecurityLocked) {
					t.Fatal("new sweep implicitly unlocked an incident", err)
				}
			}
		})
	}
}

func TestGuardedSignatureRequiresSingleSweepCapability(t *testing.T) {
	m, mock, raw := guardedManager(t)
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	securityRead(t, m, mock, emptySecurityState(), "FOR SHARE")
	mock.ExpectQuery("SELECT COALESCE.*FROM settings.*FOR SHARE").WithArgs("crypto.config").WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	mock.ExpectRollback()
	auditOK(mock, "sign_evm", "denied")
	if signed, err := m.SignEVM(t.Context(), sweepRequest()); signed != "" || !errors.Is(err, ErrSigningAuthorizationRequired) {
		t.Fatal("wallet signed outside an active sweep", err)
	}
}

func TestBroadcastRetainsVaultGateUntilActualSendReturns(t *testing.T) {
	m, mock, raw := guardedManager(t)
	ctx, _ := newGrantContext(t, m)
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	securityRead(t, m, mock, emptySecurityState(), "FOR SHARE")
	mock.ExpectQuery("SELECT COALESCE.*FROM settings.*FOR SHARE").WithArgs("crypto.config").WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	mock.ExpectRollback()
	calls := 0
	hash, err := m.GuardSweepBroadcast(ctx, func() (string, error) {
		calls++
		if m.mu.TryLock() {
			m.mu.Unlock()
			t.Fatal("broadcast released manager gate before send")
		}
		if m.db.Stats().InUse != 1 {
			t.Fatal("broadcast released vault transaction before send")
		}
		return "fixture-hash", nil
	})
	if err != nil || hash != "fixture-hash" || calls != 1 {
		t.Fatal("authorized send failed", hash, err, calls)
	}
	if m.db.Stats().InUse != 0 {
		t.Fatal("broadcast retained transaction after send returned")
	}
}

func TestPersistedIncidentPreventsBroadcastCallback(t *testing.T) {
	m, mock, raw := guardedManager(t)
	ctx, _ := newGrantContext(t, m)
	state := emptySecurityState()
	state.Locked = true
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	securityRead(t, m, mock, state, "FOR SHARE")
	mock.ExpectRollback()
	calls := 0
	if _, err := m.GuardSweepBroadcast(ctx, func() (string, error) { calls++; return "", nil }); !errors.Is(err, ErrSecurityLocked) || calls != 0 {
		t.Fatal("persistent lock crossed send boundary", calls, err)
	}
}
