package wallet

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func legacyFixtureTransaction(req EVMSignRequest) (*types.Transaction, error) {
	amount, price, err := validateEVMRequest(req)
	if err != nil {
		return nil, err
	}
	to := common.HexToAddress(req.Token)
	data := append([]byte{}, erc20TransferSelector...)
	data = append(data, common.LeftPadBytes(common.HexToAddress(req.To).Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
	return types.NewTx(&types.DynamicFeeTx{ChainID: big.NewInt(req.ChainID), Nonce: req.Nonce, GasTipCap: price, GasFeeCap: price, Gas: req.GasLimit, To: &to, Value: new(big.Int), Data: data}), nil
}

func TestRecoveredInstanceCannotSignAgainstPersistentlyDisabledChannel(t *testing.T) {
	m, mock, raw := guardedManager(t)
	m.SetEnabled(true)
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	securityRead(t, m, mock, emptySecurityState(), "FOR SHARE")
	mock.ExpectQuery("SELECT COALESCE.*FROM settings.*FOR SHARE").WithArgs("crypto.config").WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(false))
	mock.ExpectRollback()
	auditOK(mock, "sign_evm", "denied")
	if signed, err := m.SignEVM(t.Context(), sweepRequest()); signed != "" || !errors.Is(err, ErrSigningDisabled) {
		t.Fatal("stale runtime switch bypassed channel pause", err)
	}
}

func TestSignedSweepAuthorizationBindsTransactionAndTransferFields(t *testing.T) {
	m, mock, raw := guardedManager(t)
	req := sweepRequest()
	state := emptySecurityState()
	m.securityStatus = securityView(state)
	ctx, revoke, err := m.AuthorizeSweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	securityRead(t, m, mock, state, "FOR SHARE")
	mock.ExpectQuery("SELECT COALESCE.*FROM settings.*FOR SHARE").WithArgs("crypto.config").WillReturnRows(sqlmock.NewRows([]string{"enabled"}).AddRow(true))
	var authorization string
	mock.ExpectExec("INSERT INTO crypto_signer_audit").WithArgs("sign_evm", req.Kind, req.Index, req.ChainID, req.To, req.Token, req.Amount, "ok", sealedArg{capture: &authorization}).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	rawTx, err := m.SignEVM(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	tx := decodeTransaction(t, rawTx)
	if authorization != m.signatureAuthorizationReason(req.ChainID, tx.Hash().Hex()) {
		t.Fatal("signed hash lacks durable authenticated authorization")
	}
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(req.ChainID)), tx)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, hash, amount, from string
		exists, want             bool
	}{
		{"manual sweep is authorized", tx.Hash().Hex(), req.Amount, sender.Hex(), true, true},
		{"same destination different tx", "0x" + strings.Repeat("ff", 32), req.Amount, sender.Hex(), false, false},
		{"different transfer amount", tx.Hash().Hex(), "999", sender.Hex(), false, false},
		{"different sender", tx.Hash().Hex(), req.Amount, testDestination, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock.ExpectBegin()
			expectRead(mock, raw, " FOR SHARE")
			securityRead(t, m, mock, state, "FOR SHARE")
			rows := sqlmock.NewRows([]string{"index", "kind"})
			if test.exists {
				rows.AddRow(req.Index, "sweep")
			}
			mock.ExpectQuery("SELECT derivation_index,kind FROM crypto_signer_audit").WithArgs(req.ChainID, m.signatureAuthorizationReason(req.ChainID, test.hash), req.To, req.Token, test.amount).WillReturnRows(rows)
			mock.ExpectRollback()
			ok, err := m.AuthorizedTokenTransfer(t.Context(), "bsc", req.ChainID, test.hash, test.from, req.To, req.Token, test.amount)
			if err != nil || ok != test.want {
				t.Fatal("incorrect transfer authorization", ok, err)
			}
		})
	}
	if m.signatureAuthorizationReason(1, tx.Hash().Hex()) == authorization {
		t.Fatal("authorization crossed chains")
	}
}

func TestSecurityInitializationRegistersVaultAndCapturesAuditedLegacyRawTransaction(t *testing.T) {
	m, mock := mockManager(t)
	m.EnableSecurityMonitoring()
	raw := managedVault(t)
	if err := m.refreshContents(raw); err != nil {
		t.Fatal(err)
	}
	req := sweepRequest()
	// Independent raw transaction fixture signed before the new guard existed.
	key, from, err := m.deposit.EVMKey(req.Index)
	if err != nil {
		t.Fatal(err)
	}
	to := strings.ToLower(req.Token)
	unsigned, err := legacyFixtureTransaction(req)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(big.NewInt(req.ChainID)), key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := signed.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	securityVaultRead(mock, raw)
	securityRead(t, m, mock, nil, "FOR UPDATE")
	snapshotRead(mock, addressRows())
	mock.ExpectQuery("SELECT chain,from_address,to_address,token,raw_amount,derivation_index,sweep_tx_hash,sweep_raw_tx").WillReturnRows(sqlmock.NewRows([]string{"chain", "from", "to", "token", "amount", "index", "hash", "raw"}).AddRow("bsc", from.Hex(), req.To, to, req.Amount, req.Index, signed.Hash().Hex(), "0x"+hex.EncodeToString(encoded)))
	mock.ExpectQuery("SELECT EXISTS.*FROM crypto_signer_audit").WithArgs(req.ChainID, req.Index, req.To, to, req.Amount).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	var persisted, newVault string
	mock.ExpectExec("INSERT INTO settings").WithArgs(settingSecurity, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE settings SET value").WithArgs(settingVault, sealedArg{capture: &newVault}).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	status, err := m.CheckSecurity(t.Context())
	if err != nil || !status.Initialized || status.Locked {
		t.Fatal("upgrade baseline failed", status, err)
	}
	contents, err := m.openContents(newVault)
	if err != nil || contents.SecurityVersion != 1 {
		t.Fatal("vault does not remember security registration", err)
	}
	state, err := m.decodeSecurity(persisted)
	if err != nil {
		t.Fatal(err)
	}
	proof, exists := state.Legacy["bsc:"+strings.ToLower(signed.Hash().Hex())]
	if !exists || !proof.matches(req.ChainID, from.Hex(), req.To, to, req.Amount) {
		t.Fatal("legacy raw transaction was not tied to its successful audit")
	}
}
