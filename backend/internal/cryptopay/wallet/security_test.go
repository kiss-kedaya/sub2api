package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
)

func guardedManager(t *testing.T) (*Manager, sqlmock.Sqlmock, string) {
	t.Helper()
	m, mock := mockManager(t)
	m.EnableSecurityMonitoring()
	raw, err := m.sealVaultContents(vaultContents{Deposit: testDeposit, Hot: testHot, SecurityVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.refreshContents(raw); err != nil {
		t.Fatal(err)
	}
	return m, mock, raw
}

func securityRead(t *testing.T, m *Manager, mock sqlmock.Sqlmock, state *securityState, lock string) {
	t.Helper()
	rows := sqlmock.NewRows([]string{"value"})
	if state != nil {
		raw, err := m.encodeSecurity(state)
		if err != nil {
			t.Fatal(err)
		}
		rows.AddRow(raw)
	}
	mock.ExpectQuery("SELECT value FROM settings WHERE key.*" + lock).WithArgs(settingSecurity).WillReturnRows(rows)
}

func securityAddress(t *testing.T, m *Manager, user, index int64) addressSnapshot {
	t.Helper()
	_, addr, err := m.deposit.EVMKey(uint32(index))
	if err != nil {
		t.Fatal(err)
	}
	return addressSnapshot{ID: index + 10, UserID: user, Family: addresses.FamilyEVM, Index: index, Address: strings.ToLower(addr.Hex())}
}

func addressRows(entries ...addressSnapshot) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"kind", "id", "user_id", "family", "index", "address"})
	for _, row := range entries {
		kind := "assignment"
		if row.UserID == 0 {
			kind = "pool"
		}
		rows.AddRow(kind, row.ID, row.UserID, row.Family, row.Index, row.Address)
	}
	return rows
}

func snapshotRead(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT 'pool'.*UNION ALL SELECT 'assignment'").WillReturnRows(rows).RowsWillBeClosed()
}

func securityVaultRead(mock sqlmock.Sqlmock, raw string) {
	mock.ExpectQuery("SELECT value FROM settings WHERE key.*FOR UPDATE").WithArgs(settingVault).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
}

func TestSecuritySnapshotMacRejectsTamperingWrongKeyAndTruncation(t *testing.T) {
	m := New(nil, testMasterKey)
	state := emptySecurityState()
	state.Locked = true
	state.Reason = "unauthorized_token_outflow"
	raw, err := m.encodeSecurity(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{strings.Replace(raw, "unauthorized_token_outflow", "deposit_address_changed", 1), raw[:len(raw)-1], `{"payload":{},"mac":""}`} {
		if _, err := m.decodeSecurity(candidate); !errors.Is(err, ErrSecurityLocked) {
			t.Fatal("unauthenticated state accepted", err)
		}
	}
	if _, err := New(nil, strings.Repeat("ab", 32)).decodeSecurity(raw); !errors.Is(err, ErrSecurityLocked) {
		t.Fatal("wrong installation key accepted")
	}
	decoded, err := m.decodeSecurity(raw)
	if err != nil || !decoded.Locked {
		t.Fatal("authentic persistent incident rejected", err)
	}
}

func TestSecurityNewDerivedAddressExtendsSnapshotWithoutFalseIncident(t *testing.T) {
	m, mock, raw := guardedManager(t)
	state := emptySecurityState()
	existing := securityAddress(t, m, 100, 1)
	added := securityAddress(t, m, 101, 2)
	state.Assignments[snapshotKey(existing.UserID, existing.Family)] = existing
	mock.ExpectBegin()
	securityVaultRead(mock, raw)
	securityRead(t, m, mock, state, "FOR UPDATE")
	snapshotRead(mock, addressRows(existing, added))
	var persisted string
	mock.ExpectExec("INSERT INTO settings").WithArgs(settingSecurity, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	status, err := m.CheckSecurity(t.Context())
	if err != nil || status.Locked || !status.Initialized || status.DepositsPaused {
		t.Fatal("legitimate assignment paused payments", status, err)
	}
	got, err := m.decodeSecurity(persisted)
	if err != nil || len(got.Assignments) != 2 {
		t.Fatal("new address was not authenticated before use", err)
	}
}

func TestChangedOrDeletedAddressPersistsLockAndSurvivesRuntimeSwitches(t *testing.T) {
	for _, kind := range []string{"replace_assignment", "delete_assignment", "replace_pool", "delete_pool"} {
		t.Run(kind, func(t *testing.T) {
			m, mock, raw := guardedManager(t)
			state := emptySecurityState()
			old := securityAddress(t, m, 100, 1)
			changed := securityAddress(t, m, 100, 2)
			changed.ID = old.ID
			reason := "deposit_address_changed"
			if strings.Contains(kind, "pool") {
				old.UserID, changed.UserID = 0, 0
				state.Pool[poolSnapshotKey(old.Index, old.Family)] = old
				reason = "deposit_pool_changed"
			} else {
				state.Assignments[snapshotKey(old.UserID, old.Family)] = old
			}
			rows := addressRows(changed)
			if strings.HasPrefix(kind, "delete") {
				rows = addressRows()
			}
			mock.ExpectBegin()
			securityVaultRead(mock, raw)
			securityRead(t, m, mock, state, "FOR UPDATE")
			snapshotRead(mock, rows)
			var persisted string
			mock.ExpectExec("INSERT INTO settings").WithArgs(settingSecurity, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("INSERT INTO crypto_signer_audit").WithArgs("security_lock", "", 0, 0, "", "", "", "locked", reason).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
			status, err := m.CheckSecurity(t.Context())
			if !errors.Is(err, ErrSecurityLocked) || !status.Locked || !status.DepositsPaused || !status.AlertPending {
				t.Fatal("address incident did not persist", status, err)
			}
			locked, err := m.decodeSecurity(persisted)
			if err != nil || !locked.Locked {
				t.Fatal("missing durable lock", err)
			}
			// Reopen the manager exactly as a process restart would, then try
			// both runtime enable and key cleanup. Neither may reset the row.
			restarted := New(m.db, testMasterKey)
			restarted.EnableSecurityMonitoring()
			_ = restarted.SetDestination(testDestination)
			restarted.SetEnabled(true)
			restarted.Close()
			mock.ExpectBegin()
			expectRead(mock, raw, " FOR SHARE")
			securityRead(t, restarted, mock, locked, "FOR SHARE")
			mock.ExpectRollback()
			auditOK(mock, "sign_evm", "denied")
			if signed, err := restarted.SignEVM(t.Context(), sweepRequest()); signed != "" || !errors.Is(err, ErrSecurityLocked) {
				t.Fatal("runtime switch bypassed persistent lock", err)
			}
		})
	}
}

func TestDeletingOrForgingRegisteredSnapshotPersistsIncident(t *testing.T) {
	for _, forged := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "forged"}[forged], func(t *testing.T) {
			m, mock, raw := guardedManager(t)
			mock.ExpectBegin()
			securityVaultRead(mock, raw)
			rows := sqlmock.NewRows([]string{"value"})
			reason := "security_snapshot_missing"
			if forged {
				rows.AddRow(`{"payload":{},"mac":"00"}`)
				reason = "security_snapshot_authentication_failed"
			}
			mock.ExpectQuery("SELECT value FROM settings WHERE key.*FOR UPDATE").WithArgs(settingSecurity).WillReturnRows(rows)
			mock.ExpectExec("INSERT INTO settings").WithArgs(settingSecurity, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("INSERT INTO crypto_signer_audit").WithArgs("security_lock", "", 0, 0, "", "", "", "locked", reason).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
			status, err := m.CheckSecurity(t.Context())
			if !errors.Is(err, ErrSecurityLocked) || status.Reason != reason {
				t.Fatal("snapshot deletion/tamper silently rebuilt baseline", status, err)
			}
		})
	}
}

func TestDuplicateNormalizedAddressRowsCannotHideMutation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed_last", true: "trusted_last"}[reverse], func(t *testing.T) {
			m, mock, raw := guardedManager(t)
			state := emptySecurityState()
			old := securityAddress(t, m, 100, 1)
			changed := securityAddress(t, m, 100, 2)
			state.Assignments[snapshotKey(old.UserID, old.Family)] = old
			rows := addressRows(old, changed)
			if reverse {
				rows = addressRows(changed, old)
			}
			mock.ExpectBegin()
			securityVaultRead(mock, raw)
			securityRead(t, m, mock, state, "FOR UPDATE")
			snapshotRead(mock, rows)
			mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("INSERT INTO crypto_signer_audit").WithArgs("security_lock", "", 0, 0, "", "", "", "locked", "deposit_address_derivation_mismatch").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
			status, err := m.CheckSecurity(t.Context())
			if !errors.Is(err, ErrSecurityLocked) || !status.Locked {
				t.Fatal("duplicate normalized ownership silently overwrote a snapshot", status, err)
			}
		})
	}
}

func TestSecurityPersistenceFailureStaysLocallyLocked(t *testing.T) {
	m, mock, raw := guardedManager(t)
	mock.ExpectBegin()
	securityVaultRead(mock, raw)
	securityRead(t, m, mock, emptySecurityState(), "FOR UPDATE")
	mock.ExpectExec("INSERT INTO settings").WillReturnError(errors.New("storage unavailable"))
	mock.ExpectRollback()
	if err := m.LockSecurity(t.Context(), "unauthorized_token_outflow", "bsc", "0x01"); !errors.Is(err, ErrVaultUnavailable) {
		t.Fatal(err)
	}
	m.SetEnabled(true)
	m.Close()
	if !m.SecurityLocked() {
		t.Fatal("failed persistence cleared local incident")
	}
	// A later clean-looking DB read must retry the pending incident, never
	// override it with an unlocked snapshot.
	mock.ExpectBegin()
	securityVaultRead(mock, raw)
	securityRead(t, m, mock, emptySecurityState(), "FOR UPDATE")
	mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO crypto_signer_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	status, err := m.CheckSecurity(t.Context())
	if !errors.Is(err, ErrSecurityLocked) || !status.Locked {
		t.Fatal("lost failed-write incident", status, err)
	}
}

func TestSecurityAlertRetriesFailureAndDoesNotRepeatSuccessfulDelivery(t *testing.T) {
	m, mock, _ := guardedManager(t)
	state := emptySecurityState()
	state.Locked = true
	state.Reason = "unauthorized_token_outflow"
	calls := 0
	for _, fails := range []bool{true, false, false} {
		mock.ExpectBegin()
		securityRead(t, m, mock, state, "FOR UPDATE")
		if state.Alerted || fails {
			mock.ExpectRollback()
		} else {
			mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
		}
		err := m.DeliverSecurityAlert(t.Context(), func(SecurityStatus) error {
			calls++
			if fails {
				return errors.New("Bark unavailable")
			}
			return nil
		})
		if fails && err == nil {
			t.Fatal("failed alert acknowledged")
		}
		if !fails && err != nil {
			t.Fatal(err)
		}
		if !fails {
			state.Alerted = true
		}
	}
	if calls != 2 {
		t.Fatal("successful notification repeated", calls)
	}
}

func TestRecoveryRefusesUnrepairedAddressAndKeepsSigningOff(t *testing.T) {
	m, mock, raw := guardedManager(t)
	state := emptySecurityState()
	state.Locked = true
	bad := securityAddress(t, m, 100, 1)
	bad.Address = testDestination
	mock.ExpectBegin()
	securityVaultRead(mock, raw)
	securityRead(t, m, mock, state, "FOR UPDATE")
	snapshotRead(mock, addressRows(bad))
	mock.ExpectRollback()
	if err := m.RecoverSecurity(t.Context()); err == nil {
		t.Fatal("recovery accepted a nonderived address")
	}
	m.SetEnabled(false)
	good := securityAddress(t, m, 100, 1)
	mock.ExpectBegin()
	securityVaultRead(mock, raw)
	securityRead(t, m, mock, state, "FOR UPDATE")
	snapshotRead(mock, addressRows(good))
	mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO crypto_signer_audit").WithArgs("security_recover", "", 0, 0, "", "", "", "ok", "").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := m.RecoverSecurity(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.enabled || m.SecurityLocked() {
		t.Fatal("recovery unexpectedly enabled signing or kept incident lock")
	}
}

func TestSecurityStatusAlwaysIncludesBooleanFields(t *testing.T) {
	data, err := json.Marshal(SecurityStatus{})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"enabled", "initialized", "locked", "deposits_paused", "alert_pending"} {
		if !strings.Contains(string(data), `"`+field+`":false`) {
			t.Fatal("missing false boolean", field)
		}
	}
}

// Ensure cancellation cannot hand out an assignment after the guard fails.
func TestAssignmentFailsBeforeAddressReleaseWhenContextCanceled(t *testing.T) {
	m, _, _ := guardedManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.ValidateAssignment(ctx, addresses.Assignment{}); err == nil {
		t.Fatal("canceled guard released an address")
	}
}

func TestHotMnemonicImportPreservesRegisteredSecurityOnColdInstance(t *testing.T) {
	m, mock, raw := guardedManager(t)
	m.securityRegistered = false // instance cache has not read the registered ciphertext
	expectImportStart(mock, raw, false)
	expectAddresses(mock, nil)
	var persisted string
	mock.ExpectExec("UPDATE settings SET value").WithArgs(settingVault, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(1, 1))
	auditOK(mock, "hot_mnemonic_import", "success")
	mock.ExpectCommit()
	if err := m.ImportHotMnemonic(t.Context(), testReplacement); err != nil {
		t.Fatal(err)
	}
	contents, err := m.openContents(persisted)
	if err != nil || contents.SecurityVersion != 1 || !m.securityRegistered {
		t.Fatal("hot import downgraded security registration", err)
	}
}
