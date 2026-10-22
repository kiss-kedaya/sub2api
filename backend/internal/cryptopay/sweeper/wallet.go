package sweeper

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// TransactionWallet is the local key holder used by the durable sweeper.
// It has no HTTP surface and exposes no arbitrary transaction-signing route.
type TransactionWallet interface {
	SignEVM(context.Context, EVMSignRequest) (string, error)
	HotAddresses(context.Context) (*HotAddress, error)
}

type EVMSignRequest = wallet.EVMSignRequest
type HotAddress = wallet.HotAddress
type MnemonicExport = wallet.MnemonicExport

// Record is one sweep_records row.
type Record struct {
	ID          int64
	Chain       string
	FromAddress string
	ToAddress   string
	Token       string
	Amount      string
	GasTxHash   string
	SweepTxHash string
	Status      string
	Error       string
}
