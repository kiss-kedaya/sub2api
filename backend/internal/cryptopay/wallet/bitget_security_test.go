package wallet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// Every key used here is derived from the existing public BIP39 test vector.
// These tests exercise only sqlmock and local signatures; no provider or RPC.
type bitgetSecurityFixture struct {
	jobID  int64
	order  string
	in     bitget.Intent
	signed string
	mac    string
	status string
}

func newBitgetSecurityFixture(t *testing.T, m *Manager) bitgetSecurityFixture {
	t.Helper()
	const index = uint32(7)
	key, address, err := m.deposit.EVMKey(index)
	require.NoError(t, err)
	// Best-effort cleanup of a scalar derived from the public test vector.
	defer clear(key.D.Bits()) //nolint:staticcheck // geth secp256k1 uses the raw scalar.
	digest := crypto.Keccak256([]byte("sub2api/bitget/offline-security-fixture"))
	sig, err := crypto.Sign(digest, key)
	require.NoError(t, err)
	sig[64] += 27
	signed, err := json.Marshal([]bitget.Message{{Type: "call", Hash: "0x" + hex.EncodeToString(digest), Signature: "0x" + hex.EncodeToString(sig)}})
	require.NoError(t, err)
	f := bitgetSecurityFixture{
		jobID: 77, order: "offline-bitget-order", signed: string(signed), status: "sweep_pending",
		in: bitget.Intent{
			Chain: "bsc", ChainID: 56, Index: index, From: address.Hex(), To: testDestination,
			Token: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18,
			Gross: "10000000000000000000", Net: "9950000000000000000", Fee: "50000000000000000",
			MaxFee: "0.5", MaxFeeBPS: 100, Nonce: 7, CreatedAt: time.Now().Unix(),
		},
	}
	f.mac = m.bitgetAuthorization(f.jobID, f.order, f.in, f.signed)
	return f
}

func bitgetSecurityRows(t *testing.T, f bitgetSecurityFixture) *sqlmock.Rows {
	t.Helper()
	raw, err := json.Marshal(f.in)
	require.NoError(t, err)
	return sqlmock.NewRows([]string{
		"order_id", "intent", "signed_messages", "authorization_mac", "chain", "from_address", "to_address",
		"token", "raw_amount", "derivation_index", "job_version", "token_decimals", "status",
	}).AddRow(f.order, raw, f.signed, f.mac, f.in.Chain, f.in.From, f.in.To, f.in.Token, f.in.Gross,
		int64(f.in.Index), 2, f.in.Decimals, f.status)
}

func expectBitgetSecurityRecord(t *testing.T, mock sqlmock.Sqlmock, jobID int64, f bitgetSecurityFixture) {
	t.Helper()
	mock.ExpectQuery("SELECT b.order_id,b.intent,b.signed_messages,b.authorization_mac").
		WithArgs(jobID).WillReturnRows(bitgetSecurityRows(t, f))
}

func TestBitgetRecordObservationRequiresAuthenticRecordAndExactCallerSnapshot(t *testing.T) {
	for _, name := range []string{
		"authentic without signing grant", "observer without message snapshot", "job transplant", "stored order", "stored intent", "stored messages", "stored MAC",
		"caller order", "caller intent", "caller messages",
	} {
		t.Run(name, func(t *testing.T) {
			m, mock, _ := guardedManager(t)
			f := newBitgetSecurityFixture(t, m)
			jobID, id, in, signed := f.jobID, f.order, f.in, f.signed
			switch name {
			case "observer without message snapshot":
				signed = "" // A monitor still validates the stored signature and intent.
			case "job transplant":
				jobID++ // A valid order MAC cannot move to another sweep row.
			case "stored order":
				f.order += "-changed"
			case "stored intent":
				f.in.To = "0x3333333333333333333333333333333333333333"
			case "stored messages":
				f.signed += " "
			case "stored MAC":
				f.mac = strings.Repeat("0", 64)
			case "caller order":
				id += "-changed"
			case "caller intent":
				in.Fee = "0"
			case "caller messages":
				signed += " "
			}
			expectBitgetSecurityRecord(t, mock, jobID, f)
			err := m.VerifyBitgetRecord(t.Context(), jobID, id, in, signed)
			if name == "authentic without signing grant" || name == "observer without message snapshot" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, bitget.ErrUnsafeOrder)
			}
		})
	}
}

func TestBitgetRecordRejectsMissingDurableAuthorization(t *testing.T) {
	m, mock, _ := guardedManager(t)
	f := newBitgetSecurityFixture(t, m)
	mock.ExpectQuery("SELECT b.order_id,b.intent,b.signed_messages,b.authorization_mac").WithArgs(f.jobID).
		WillReturnRows(sqlmock.NewRows([]string{"order_id", "intent", "signed_messages", "authorization_mac", "chain", "from_address", "to_address", "token", "raw_amount", "derivation_index", "job_version", "token_decimals", "status"}))
	require.ErrorIs(t, m.VerifyBitgetRecord(t.Context(), f.jobID, f.order, f.in, f.signed), ErrAuditUnavailable)
}

func TestBitgetRecordVerifiesRecoveredSenderEvenWithAuthenticMAC(t *testing.T) {
	m, mock, _ := guardedManager(t)
	f := newBitgetSecurityFixture(t, m)
	// Independently sign the same digest with another public-vector child key.
	var msgs []bitget.Message
	require.NoError(t, json.Unmarshal([]byte(f.signed), &msgs))
	other, _, err := m.deposit.EVMKey(f.in.Index + 1)
	require.NoError(t, err)
	defer clear(other.D.Bits()) //nolint:staticcheck // geth secp256k1 uses the raw scalar.
	digest, err := hex.DecodeString(strings.TrimPrefix(msgs[0].Hash, "0x"))
	require.NoError(t, err)
	sig, err := crypto.Sign(digest, other)
	require.NoError(t, err)
	sig[64] += 27
	msgs[0].Signature = "0x" + hex.EncodeToString(sig)
	raw, err := json.Marshal(msgs)
	require.NoError(t, err)
	f.signed = string(raw)
	f.mac = m.bitgetAuthorization(f.jobID, f.order, f.in, f.signed)
	expectBitgetSecurityRecord(t, mock, f.jobID, f)
	require.ErrorIs(t, m.VerifyBitgetRecord(t.Context(), f.jobID, f.order, f.in, f.signed), bitget.ErrUnsafeOrder)
}

func TestBitgetSignedMessagesRejectWrongSenderDigestAndSignatureEncoding(t *testing.T) {
	m, _, _ := guardedManager(t)
	f := newBitgetSecurityFixture(t, m)
	for _, name := range []string{"valid", "wrong sender", "changed digest", "short signature", "invalid recovery id", "empty messages", "missing signature"} {
		t.Run(name, func(t *testing.T) {
			in, signed := f.in, f.signed
			var msgs []bitget.Message
			require.NoError(t, json.Unmarshal([]byte(signed), &msgs))
			switch name {
			case "wrong sender":
				in.From = testDestination
			case "changed digest":
				msgs[0].Hash = "0x" + strings.Repeat("ab", 32)
			case "short signature":
				msgs[0].Signature = "0x01"
			case "invalid recovery id":
				msgs[0].Signature = msgs[0].Signature[:len(msgs[0].Signature)-2] + "00"
			case "empty messages":
				msgs = nil
			case "missing signature":
				msgs[0].Signature = ""
			}
			raw, err := json.Marshal(msgs)
			require.NoError(t, err)
			err = verifyBitgetSignedMessages(in, string(raw))
			if name == "valid" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, bitget.ErrUnsafeOrder)
			}
		})
	}
}

func TestBitgetSubmitRejectsMissingRevokedExpiredAndForeignGrants(t *testing.T) {
	for _, name := range []string{"missing", "revoked", "expired", "foreign", "channel disabled"} {
		t.Run(name, func(t *testing.T) {
			m, _, _ := guardedManager(t)
			ctx := t.Context()
			if name != "missing" {
				var revoke func()
				ctx, revoke = newGrantContext(t, m)
				switch name {
				case "revoked":
					revoke()
				case "expired":
					grant := *ctx.Value(signingGrantContextKey{}).(*signingGrant)
					grant.expires = time.Now().Add(-time.Second).UnixNano()
					grant.mac = m.grantMAC(&grant)
					ctx = context.WithValue(ctx, signingGrantContextKey{}, &grant)
				case "foreign":
					other, _, _ := guardedManager(t)
					ctx, _ = newGrantContext(t, other)
				case "channel disabled":
					m.SetEnabled(false)
				}
			}
			calls := 0
			_, err := m.GuardBitgetSubmit(ctx, 77, "offline-bitget-order", "[]", func() (string, error) { calls++; return "", nil })
			require.ErrorIs(t, err, ErrSigningAuthorizationRequired)
			require.Zero(t, calls)
		})
	}
}

func bitgetSubmitConfig(in bitget.Intent) map[string]any {
	return map[string]any{
		"enabled": true, "sweep_provider": "bitget", "hot_wallet_evm": in.To,
		"bitget_max_fee": "0.5", "bitget_max_fee_bps": 100, "chains": []string{in.Chain},
	}
}

func expectBitgetSubmitVault(t *testing.T, m *Manager, mock sqlmock.Sqlmock, raw string, f bitgetSecurityFixture) {
	t.Helper()
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	securityRead(t, m, mock, emptySecurityState(), "FOR SHARE")
	expectBitgetSecurityRecord(t, mock, f.jobID, f)
}

func TestBitgetSubmitRechecksCurrentProviderDestinationChainAndFeeLimits(t *testing.T) {
	for _, name := range []string{"allowed", "channel disabled", "native provider", "destination changed", "chain disabled", "amount limit lowered", "percentage limit lowered", "malformed limit", "invalid percentage", "missing config"} {
		t.Run(name, func(t *testing.T) {
			m, mock, raw := guardedManager(t)
			ctx, _ := newGrantContext(t, m)
			f := newBitgetSecurityFixture(t, m)
			cfg := bitgetSubmitConfig(f.in)
			want := ErrSigningDisabled
			switch name {
			case "channel disabled":
				cfg["enabled"] = false
			case "native provider":
				cfg["sweep_provider"] = "native"
			case "destination changed":
				cfg["hot_wallet_evm"] = "0x3333333333333333333333333333333333333333"
			case "chain disabled":
				cfg["chains"] = []string{"ethereum"}
			case "amount limit lowered":
				cfg["bitget_max_fee"], want = "0.01", bitget.ErrUnsafeOrder
			case "percentage limit lowered":
				cfg["bitget_max_fee_bps"], want = 1, bitget.ErrUnsafeOrder
			case "malformed limit":
				cfg["bitget_max_fee"], want = "1e99999999", bitget.ErrUnsafeOrder
			case "invalid percentage":
				cfg["bitget_max_fee_bps"], want = 1001, bitget.ErrUnsafeOrder
			}
			expectBitgetSubmitVault(t, m, mock, raw, f)
			rows := sqlmock.NewRows([]string{"value"})
			if name != "missing config" {
				encoded, err := json.Marshal(cfg)
				require.NoError(t, err)
				rows.AddRow(string(encoded))
			}
			mock.ExpectQuery("SELECT value FROM settings WHERE key='crypto.config' FOR SHARE").WillReturnRows(rows)
			mock.ExpectRollback()
			calls := 0
			got, err := m.GuardBitgetSubmit(ctx, f.jobID, f.order, f.signed, func() (string, error) {
				calls++
				return "offline-submission", nil
			})
			if name == "allowed" {
				require.NoError(t, err)
				require.Equal(t, "offline-submission", got)
				require.Equal(t, 1, calls)
			} else {
				require.ErrorIs(t, err, want)
				require.Empty(t, got)
				require.Zero(t, calls)
			}
		})
	}
}

func TestBitgetSubmitRejectsStoppedExpiredOrDifferentDurableOrders(t *testing.T) {
	for _, name := range []string{"manual review", "failed", "swept", "expired", "future order", "different order", "different messages"} {
		t.Run(name, func(t *testing.T) {
			m, mock, raw := guardedManager(t)
			ctx, _ := newGrantContext(t, m)
			f := newBitgetSecurityFixture(t, m)
			id, signed := f.order, f.signed
			switch name {
			case "manual review":
				f.status = "manual_review"
			case "failed", "swept":
				f.status = name
			case "expired":
				f.in.CreatedAt = time.Now().Add(-11 * time.Minute).Unix()
			case "future order":
				f.in.CreatedAt = time.Now().Add(time.Minute).Unix()
			case "different order":
				id += "-changed"
			case "different messages":
				signed += " "
			}
			f.mac = m.bitgetAuthorization(f.jobID, f.order, f.in, f.signed)
			expectBitgetSubmitVault(t, m, mock, raw, f)
			mock.ExpectRollback()
			calls := 0
			_, err := m.GuardBitgetSubmit(ctx, f.jobID, id, signed, func() (string, error) { calls++; return "", nil })
			require.ErrorIs(t, err, bitget.ErrUnsafeOrder)
			require.Zero(t, calls)
		})
	}
}

func TestBitgetSubmitRetainsVaultAndManagerGatesUntilSendReturns(t *testing.T) {
	m, mock, raw := guardedManager(t)
	ctx, _ := newGrantContext(t, m)
	f := newBitgetSecurityFixture(t, m)
	expectBitgetSubmitVault(t, m, mock, raw, f)
	cfg, err := json.Marshal(bitgetSubmitConfig(f.in))
	require.NoError(t, err)
	mock.ExpectQuery("SELECT value FROM settings WHERE key='crypto.config' FOR SHARE").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(cfg)))
	mock.ExpectRollback()
	sendErr := errors.New("offline transport failure")
	calls := 0
	_, err = m.GuardBitgetSubmit(ctx, f.jobID, f.order, f.signed, func() (string, error) {
		calls++
		if m.mu.TryLock() {
			m.mu.Unlock()
			t.Error("Bitget submit released manager gate before the network send")
		}
		require.Equal(t, 1, m.db.Stats().InUse, "vault/config transaction must be live during send")
		return "", sendErr
	})
	require.ErrorIs(t, err, sendErr)
	require.Equal(t, 1, calls)
	require.Zero(t, m.db.Stats().InUse, "send failure must release the database transaction")
}
