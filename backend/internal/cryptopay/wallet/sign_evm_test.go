package wallet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
)

func sweepRequest() EVMSignRequest {
	return EVMSignRequest{Index: 0, Kind: "sweep", ChainID: 56, Token: testToken, To: testDestination,
		Amount: "1000000", Nonce: 7, GasPrice: "1000000000", GasLimit: 65000}
}

func signExpectation(mock sqlmock.Sqlmock, raw string, req EVMSignRequest) {
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	index := req.Index
	if req.Kind == "gas" {
		index = 0
	}
	mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs("sign_evm", req.Kind, index, req.ChainID, req.To, req.Token, req.Amount, "ok", "").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
}

func decodeTransaction(t *testing.T, raw string) *types.Transaction {
	t.Helper()
	encoded, err := hex.DecodeString(strings.TrimPrefix(raw, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(encoded); err != nil {
		t.Fatal(err)
	}
	return &tx
}

func TestOfflineSweepSignatureAndDynamicFees(t *testing.T) {
	m, mock, raw := residentManager(t)
	req := sweepRequest()
	signExpectation(mock, raw, req)
	signed, err := m.SignEVM(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	tx := decodeTransaction(t, signed)
	sender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(req.ChainID)), tx)
	if err != nil || sender.Hex() != "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" {
		t.Fatal("wrong deposit signing path", err)
	}
	wantData := append([]byte{0xa9, 0x05, 0x9c, 0xbb}, common.LeftPadBytes(common.HexToAddress(req.To).Bytes(), 32)...)
	wantData = append(wantData, common.LeftPadBytes(big.NewInt(1000000).Bytes(), 32)...)
	if tx.Type() != types.DynamicFeeTxType || tx.To().Hex() != common.HexToAddress(testToken).Hex() || tx.Value().Sign() != 0 || !bytes.Equal(tx.Data(), wantData) || tx.GasTipCap().Cmp(big.NewInt(1000000000)) != 0 || tx.GasFeeCap().Cmp(tx.GasTipCap()) != 0 {
		t.Fatal("ERC20 transaction or dynamic fee serialization changed")
	}
}

func TestExportedDefaultGasMnemonicRecoversOfflineSigningKey(t *testing.T) {
	m, mock, raw := residentManager(t)
	expectRead(mock, raw, "")
	auditOK(mock, "mnemonic_export", "success")
	value, err := m.ExportMnemonics(t.Context(), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	// Independent offline reconstruction of DEFAULT path m/44'/60'/0'/0/0.
	// Legacy nonzero policy hot_index requires its original path externally.
	seed := bip39.NewSeed(value.HotMnemonic, "")
	defer clear(seed)
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	child := master
	for _, index := range []uint32{1<<31 | 44, 1<<31 | 60, 1 << 31, 0, 0} {
		child, err = child.Derive(index)
		if err != nil {
			t.Fatal(err)
		}
	}
	priv, err := child.ECPrivKey()
	if err != nil {
		t.Fatal(err)
	}
	serialized := priv.Serialize()
	defer clear(serialized)
	recoveredKey, err := crypto.ToECDSA(serialized)
	if err != nil {
		t.Fatal(err)
	}
	expectedSender := crypto.PubkeyToAddress(recoveredKey.PublicKey)
	d, err := NewDeriver(value.DepositMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	gasIndex := uint32(7)
	_, depositAddress, err := d.EVMKey(gasIndex)
	if err != nil {
		t.Fatal(err)
	}
	req := sweepRequest()
	req.Kind = "gas"
	req.Index = 91
	req.GasIndex = &gasIndex
	req.Token = "native"
	req.To = depositAddress.Hex()
	req.GasLimit = 21000
	signExpectation(mock, raw, req)
	signed, err := m.SignEVM(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	tx := decodeTransaction(t, signed)
	actualSender, err := types.Sender(types.LatestSignerForChainID(big.NewInt(56)), tx)
	if err != nil || actualSender != expectedSender || len(tx.Data()) != 0 || tx.To().Hex() != req.To || tx.Value().Cmp(big.NewInt(1000000)) != 0 {
		t.Fatal("exported default hot key cannot recover gas sender", err)
	}
	offline, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{ChainID: tx.ChainId(), Nonce: tx.Nonce(), GasTipCap: tx.GasTipCap(), GasFeeCap: tx.GasFeeCap(), Gas: tx.Gas(), To: tx.To(), Value: tx.Value()}), types.LatestSignerForChainID(tx.ChainId()), recoveredKey)
	if err != nil || offline.Hash() != tx.Hash() {
		t.Fatal("offline restored hot key cannot reproduce signature", err)
	}
}

func TestSignRejectsLockedDisabledWrongDestinationsAndUnauthorizedGas(t *testing.T) {
	for _, name := range []string{"locked", "disabled", "native sweep", "wrong destination", "gas missing index", "gas to arbitrary address", "gas token"} {
		t.Run(name, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			req := sweepRequest()
			switch name {
			case "locked":
				m.Lock()
			case "disabled":
				m.SetEnabled(false)
			default:
				switch name {
				case "native sweep":
					req.Token = "native"
				case "wrong destination":
					req.To = testToken
				default:
					req.Kind = "gas"
					req.Token = "native"
					req.GasLimit = 21000
					index := uint32(3)
					req.GasIndex = &index
					if name == "gas missing index" {
						req.GasIndex = nil
					}
					if name == "gas token" {
						req.Token = testToken
					}
				}
				mock.ExpectBegin()
				expectRead(mock, raw, " FOR SHARE")
				mock.ExpectRollback()
			}
			auditOK(mock, "sign_evm", "denied")
			if signed, err := m.SignEVM(t.Context(), req); signed != "" || err == nil {
				t.Fatal("unauthorized transaction signed")
			}
		})
	}
}

func TestSigningAuditFailureWithholdsSignedBytes(t *testing.T) {
	m, mock, raw := residentManager(t)
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WillReturnError(errors.New(testPassphrase + testHot))
	mock.ExpectRollback()
	if signed, err := m.SignEVM(t.Context(), sweepRequest()); signed != "" || !errors.Is(err, ErrAuditUnavailable) || strings.Contains(err.Error(), testHot) {
		t.Fatal("audit failure released signature or secret", err)
	}
}

func TestBasicTransactionValidationRejectsMalformedIntegersAndBounds(t *testing.T) {
	mutations := map[string]func(*EVMSignRequest){
		"unsupported chain":     func(r *EVMSignRequest) { r.ChainID = 1 },
		"wrong sweep gas":       func(r *EVMSignRequest) { r.GasLimit = 21000 },
		"wrong gas topup limit": func(r *EVMSignRequest) { r.Kind = "gas"; r.GasLimit = 65000 },
		"chain":                 func(r *EVMSignRequest) { r.ChainID = 0 },
		"index":                 func(r *EVMSignRequest) { r.Index = 1 << 31 },
		"kind":                  func(r *EVMSignRequest) { r.Kind = "other" },
		"target":                func(r *EVMSignRequest) { r.To = "bad" },
		"token":                 func(r *EVMSignRequest) { r.Token = "bad" },
		"zero amount":           func(r *EVMSignRequest) { r.Amount = "0" },
		"negative amount":       func(r *EVMSignRequest) { r.Amount = "-1" },
		"hex amount":            func(r *EVMSignRequest) { r.Amount = "0x10" },
		"overflow amount":       func(r *EVMSignRequest) { r.Amount = new(big.Int).Lsh(big.NewInt(1), 256).String() },
		"negative fee":          func(r *EVMSignRequest) { r.GasPrice = "-1" },
		"fee overflow": func(r *EVMSignRequest) {
			r.GasPrice = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)).String()
		},
		"low gas":  func(r *EVMSignRequest) { r.GasLimit = 20999 },
		"high gas": func(r *EVMSignRequest) { r.GasLimit = 1000001 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			req := sweepRequest()
			mutate(&req)
			if _, _, err := validateEVMRequest(req); err == nil {
				t.Fatal("invalid transaction accepted")
			}
		})
	}
}
