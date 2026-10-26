// Package bitget implements the Bitget Wallet gasless transfer order protocol.
// Keys stay in the existing local vault; only signed, validated orders leave it.
package bitget

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
)

const Provider = "bitget"

var ErrUnavailable = errors.New("Bitget gasless service is unavailable")
var ErrUnsafeOrder = errors.New("Bitget order is missing verifiable signing data or exceeds sweep policy")
var ErrUnsupportedChain = errors.New("Bitget gasless transfer is not supported on this chain")

func Chain(chain string) (string, int64) {
	switch chain {
	case "ethereum":
		return "eth", 1
	case "bsc":
		return "bnb", 56
	case "polygon":
		return "matic", 137
	case "arbitrum":
		return "arbitrum", 42161
	case "base":
		return "base", 8453
	default:
		return "", 0
	}
}

// SupportedTokenDecimals pins gas payment to the existing stablecoin catalog.
// A response or persisted job cannot redefine base units for a known token.
func SupportedTokenDecimals(chain, token string) (int, bool) {
	for _, spec := range supportedTokens[chain] {
		if strings.EqualFold(spec.contract, token) {
			return spec.decimals, true
		}
	}
	return 0, false
}

type tokenSpec struct {
	contract string
	decimals int
}

var supportedTokens = map[string][]tokenSpec{
	"ethereum": {{"0xdac17f958d2ee523a2206206994597c13d831ec7", 6}, {"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", 6}},
	"bsc":      {{"0x55d398326f99059ff775485246999027b3197955", 18}, {"0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", 18}},
	"polygon":  {{"0xc2132d05d31c914a87c6611c10748aeb04b58e8f", 6}, {"0x3c499c542cef5e3811e1192ce70d8cc03d5c3359", 6}},
	"arbitrum": {{"0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9", 6}, {"0xaf88d065e77c8cc2239327c5edb3a432268e5831", 6}},
	"base":     {{"0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", 6}},
}

type Request struct {
	Chain    string `json:"chain"`
	Contract string `json:"contract"`
	From     string `json:"from"`
	To       string `json:"to"`
	Amount   string `json:"amount"`
	NoGas    bool   `json:"noGas"`
	PayToken string `json:"noGasPayToken"`
}

type Message struct {
	Type      string                     `json:"msgType"`
	Hash      string                     `json:"hash"`
	Data      json.RawMessage            `json:"msgData,omitempty"`
	Signature string                     `json:"sig,omitempty"`
	Extra     map[string]json.RawMessage `json:"-"`
}

func (m *Message) UnmarshalJSON(raw []byte) error {
	type fields Message
	var f fields
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	*m = Message(f)
	if err := json.Unmarshal(raw, &m.Extra); err != nil {
		return err
	}
	for _, k := range []string{"msgType", "hash", "msgData", "sig"} {
		delete(m.Extra, k)
	}
	return nil
}
func (m Message) MarshalJSON() ([]byte, error) {
	type fields Message
	raw, err := json.Marshal(fields(m))
	if err != nil {
		return nil, err
	}
	var f map[string]json.RawMessage
	if err = json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	for k, v := range m.Extra {
		if _, ok := f[k]; !ok {
			f[k] = v
		}
	}
	return json.Marshal(f)
}

type Order struct {
	Attestation    *Attestation `json:"attestation,omitempty"`
	ID             string       `json:"orderId"`
	Chain          string       `json:"chain"`
	From           string       `json:"from"`
	To             string       `json:"to"`
	Contract       string       `json:"contract"`
	Amount         string       `json:"amount"`
	EstimateRevert bool         `json:"estimateRevert"`
	NoGas          struct {
		Available bool   `json:"available"`
		PayToken  string `json:"payToken"`
		PayAmount string `json:"payAmount"`
		NeedAuth  bool   `json:"need7702Auth"`
		Warn      string `json:"warn"`
	} `json:"noGas"`
	Source struct {
		Type string `json:"type"`
		EVM  struct {
			Messages []Message `json:"msgToSign"`
		} `json:"evm7702"`
	} `json:"source"`
}

// Attestation retains the vendor's three signatures over the exact request and
// response bytes. The signer verifies them independently, including after a
// process restart; a database edit cannot manufacture a trusted server hash.
type Attestation struct {
	Request           []byte `json:"request"`
	Response          []byte `json:"response"`
	ResponseSignature string `json:"response_signature"`
	RequestSignature  string `json:"request_signature"`
	DoubleSignature   string `json:"double_signature"`
}

type OrderStatus struct {
	ID     string `json:"orderId"`
	Status string `json:"orderStatus"`
	Hash   string `json:"txHash"`
	TxID   string `json:"txId"`
}

// Intent is the exact maximum token debit authorized by the credited ledger.
// Net + fee must equal Gross. All quantities are integer token base units.
type Intent struct {
	Chain           string `json:"chain"`
	ChainID         int64  `json:"chain_id"`
	Index           uint32 `json:"index"`
	From            string `json:"from"`
	To              string `json:"to"`
	Token           string `json:"token"`
	Decimals        int    `json:"decimals"`
	Gross           string `json:"gross"`
	Net             string `json:"net"`
	Fee             string `json:"fee"`
	MaxFee          string `json:"max_fee"`
	MaxFeeBPS       int    `json:"max_fee_bps"`
	Nonce           uint64 `json:"nonce"`
	CurrentDelegate string `json:"current_delegate"`
}

type Validated struct {
	Digests      [][]byte
	AuthIndex    int
	Delegate     string
	Admin        string
	FeeCollector string
	Deadline     int64
	CallHash     string
}

func Uint(s string) (*big.Int, error) {
	if len(s) == 0 || len(s) > 78 {
		return nil, ErrUnsafeOrder
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, ErrUnsafeOrder
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return nil, ErrUnsafeOrder
	}
	return n, nil
}
