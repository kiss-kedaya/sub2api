package bitget

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/shopspring/decimal"
)

func RawAmount(amount string, decimals int) (*big.Int, error) {
	if decimals < 0 || decimals > 18 || len(amount) > 100 {
		return nil, ErrUnsafeOrder
	}
	for _, c := range amount {
		if (c < '0' || c > '9') && c != '.' {
			return nil, ErrUnsafeOrder
		}
	}
	d, err := decimal.NewFromString(amount)
	if err != nil || d.IsNegative() {
		return nil, ErrUnsafeOrder
	}
	scaled := d.Shift(int32(decimals))
	if !scaled.Equal(scaled.Truncate(0)) {
		return nil, ErrUnsafeOrder
	}
	return Uint(scaled.StringFixed(0))
}

func ValidAddress(s string) bool {
	return len(s) == 42 && strings.HasPrefix(s, "0x") && common.IsHexAddress(s) && common.HexToAddress(s) != (common.Address{})
}
func Hash(s string) bool {
	b, e := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	return e == nil && strings.HasPrefix(s, "0x") && len(b) == 32
}

func ValidateQuote(o *Order, in Intent) error {
	chain, id := Chain(in.Chain)
	if o == nil || id == 0 || in.ChainID != id || o.Chain != chain || o.EstimateRevert || o.Source.Type != "evm_7702" || !o.NoGas.Available || o.NoGas.Warn != "" ||
		!strings.EqualFold(o.From, in.From) || !strings.EqualFold(o.To, in.To) || !strings.EqualFold(o.Contract, in.Token) || !strings.EqualFold(o.NoGas.PayToken, in.Token) ||
		!ValidAddress(in.From) || !ValidAddress(in.To) || !ValidAddress(in.Token) || strings.EqualFold(in.From, in.To) {
		return ErrUnsafeOrder
	}
	net, e := Uint(in.Net)
	if e != nil || net.Sign() <= 0 {
		return ErrUnsafeOrder
	}
	got, e := RawAmount(o.Amount, in.Decimals)
	if e != nil || got.Cmp(net) != 0 {
		return ErrUnsafeOrder
	}
	fee, e := RawAmount(o.NoGas.PayAmount, in.Decimals)
	if e != nil {
		return ErrUnsafeOrder
	}
	want, e := Uint(in.Fee)
	if e != nil || want.Cmp(fee) != 0 {
		return ErrUnsafeOrder
	}
	gross, e := Uint(in.Gross)
	if e != nil || new(big.Int).Add(net, fee).Cmp(gross) != 0 {
		return ErrUnsafeOrder
	}
	cap, e := RawAmount(in.MaxFee, in.Decimals)
	if e != nil || cap.Sign() <= 0 || fee.Cmp(cap) > 0 || in.MaxFeeBPS < 1 || in.MaxFeeBPS > 1000 {
		return ErrUnsafeOrder
	}
	if new(big.Int).Mul(fee, big.NewInt(10000)).Cmp(new(big.Int).Mul(gross, big.NewInt(int64(in.MaxFeeBPS)))) > 0 {
		return ErrUnsafeOrder
	}
	return nil
}

// ValidateSigning reconstructs every digest. Hash-only orders, unexpected
// approvals/calls, native value, foreign delegation and unbounded expiry fail.
// An upstream change therefore cannot silently expand signing authority.
func ValidateSigning(o *Order, in Intent, now time.Time) (*Validated, error) {
	return NewClient().ValidateSigning(o, in, now)
}

func (c *Client) ValidateSigning(o *Order, in Intent, now time.Time) (*Validated, error) {
	if err := c.verifyAttestation(o, in); err != nil {
		return nil, err
	}
	if err := ValidateQuote(o, in); err != nil {
		return nil, err
	}
	msgs := o.Source.EVM.Messages
	if len(msgs) < 1 || len(msgs) > 2 {
		return nil, ErrUnsafeOrder
	}
	v := &Validated{AuthIndex: -1}
	for i, msg := range msgs {
		if msg.Signature != "" || !Hash(msg.Hash) {
			return nil, ErrUnsafeOrder
		}
		// The official transfer protocol permits vendor-attested hashes without
		// typed metadata. Never treat an unauthenticated hash as such an order.
		if len(msg.Data) == 0 || string(msg.Data) == "null" {
			if msg.Type == "auth" {
				if i != 0 || len(msgs) != 2 || !o.NoGas.NeedAuth || in.CurrentDelegate != "" {
					return nil, ErrUnsafeOrder
				}
				v.AuthIndex = i
			} else if msg.Type == "call" && i == len(msgs)-1 {
				v.CallHash = msg.Hash
			} else {
				return nil, ErrUnsafeOrder
			}
			v.Digests = append(v.Digests, common.FromHex(msg.Hash))
			continue
		}
		data, err := messageJSON(msg.Data)
		if err != nil {
			return nil, err
		}
		var digest []byte
		switch msg.Type {
		case "auth":
			if i != 0 || len(msgs) != 2 || !o.NoGas.NeedAuth {
				return nil, ErrUnsafeOrder
			}
			var a struct {
				ChainID json.RawMessage `json:"chainId"`
				Address string          `json:"address"`
				Nonce   json.RawMessage `json:"nonce"`
			}
			if json.Unmarshal(data, &a) != nil || !ValidAddress(a.Address) {
				return nil, ErrUnsafeOrder
			}
			id, e := quantity(a.ChainID)
			if e != nil || !id.IsInt64() || id.Int64() != in.ChainID {
				return nil, ErrUnsafeOrder
			}
			n, e := quantity(a.Nonce)
			if e != nil || !n.IsUint64() || n.Uint64() != in.Nonce {
				return nil, ErrUnsafeOrder
			}
			if in.CurrentDelegate != "" && !strings.EqualFold(in.CurrentDelegate, a.Address) {
				return nil, ErrUnsafeOrder
			}
			encoded, e := rlp.EncodeToBytes([]any{id, common.HexToAddress(a.Address), n.Uint64()})
			if e != nil {
				return nil, ErrUnsafeOrder
			}
			digest = crypto.Keccak256(append([]byte{0x05}, encoded...))
			v.AuthIndex = i
			v.Delegate = a.Address
		case "call":
			if i != len(msgs)-1 {
				return nil, ErrUnsafeOrder
			}
			var typed apitypes.TypedData
			if json.Unmarshal(data, &typed) != nil {
				return nil, ErrUnsafeOrder
			}
			if err := validateCalls(typed, in, v, now); err != nil {
				return nil, err
			}
			digest, _, err = apitypes.TypedDataAndHash(typed)
			if err != nil {
				return nil, ErrUnsafeOrder
			}
			v.CallHash = msg.Hash
		default:
			return nil, ErrUnsafeOrder
		}
		if !bytes.Equal(digest, common.FromHex(msg.Hash)) {
			return nil, ErrUnsafeOrder
		}
		v.Digests = append(v.Digests, digest)
	}
	if v.CallHash == "" || o.NoGas.NeedAuth != (v.AuthIndex >= 0) {
		return nil, ErrUnsafeOrder
	}
	if v.Delegate == "" {
		v.Delegate = in.CurrentDelegate
	}
	return v, nil
}

var aggregatorTypes = apitypes.Types{
	"EIP712Domain": {{Name: "name", Type: "string"}, {Name: "version", Type: "string"}, {Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}},
	"Aggregator":   {{Name: "chainId", Type: "uint256"}, {Name: "msgSender", Type: "address"}, {Name: "deadline", Type: "uint256"}, {Name: "nonce", Type: "uint256"}, {Name: "adminContract", Type: "address"}, {Name: "calls", Type: "Call[]"}},
	"Call":         {{Name: "target", Type: "address"}, {Name: "value", Type: "uint256"}, {Name: "callData", Type: "bytes"}},
}

func validateCalls(t apitypes.TypedData, in Intent, v *Validated, now time.Time) error {
	if t.PrimaryType != "Aggregator" || !reflect.DeepEqual(t.Types, aggregatorTypes) || t.Domain.Name != "BW7702Admin" || t.Domain.Version != "1" || t.Domain.Salt != "" ||
		t.Domain.ChainId == nil || (*big.Int)(t.Domain.ChainId).Cmp(big.NewInt(in.ChainID)) != 0 || !ValidAddress(t.Domain.VerifyingContract) || len(t.Message) != 6 {
		return ErrUnsafeOrder
	}
	msg := t.Message
	id, e := number(msg["chainId"])
	if e != nil || id.Cmp(big.NewInt(in.ChainID)) != 0 {
		return ErrUnsafeOrder
	}
	from, _ := msg["msgSender"].(string)
	admin, _ := msg["adminContract"].(string)
	if !strings.EqualFold(from, in.From) || !strings.EqualFold(admin, t.Domain.VerifyingContract) {
		return ErrUnsafeOrder
	}
	deadline, e := number(msg["deadline"])
	if e != nil || !deadline.IsInt64() || deadline.Int64() <= now.Unix() || deadline.Int64() > now.Add(10*time.Minute).Unix() {
		return ErrUnsafeOrder
	}
	nonce, e := number(msg["nonce"])
	if e != nil || nonce.Sign() < 0 {
		return ErrUnsafeOrder
	}
	calls, ok := msg["calls"].([]any)
	if !ok || len(calls) < 1 || len(calls) > 2 {
		return ErrUnsafeOrder
	}
	net, _ := Uint(in.Net)
	fee, _ := Uint(in.Fee)
	seenNet, seenFee := false, fee.Sign() == 0
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok || len(call) != 3 {
			return ErrUnsafeOrder
		}
		target, _ := call["target"].(string)
		value, e := number(call["value"])
		data, _ := call["callData"].(string)
		encoded, e2 := hex.DecodeString(strings.TrimPrefix(data, "0x"))
		if e != nil || value.Sign() != 0 || !strings.EqualFold(target, in.Token) || e2 != nil || len(encoded) != 68 || !strings.HasPrefix(data, "0x") || !bytes.Equal(encoded[:4], common.FromHex("0xa9059cbb")) || !bytes.Equal(encoded[4:16], make([]byte, 12)) {
			return ErrUnsafeOrder
		}
		to := common.BytesToAddress(encoded[16:36])
		amount := new(big.Int).SetBytes(encoded[36:68])
		if to == common.HexToAddress(in.To) && !seenNet && amount.Cmp(net) == 0 {
			seenNet = true
			continue
		}
		if !seenFee && to != (common.Address{}) && to != common.HexToAddress(in.From) && to != common.HexToAddress(in.To) && amount.Cmp(fee) == 0 {
			seenFee = true
			v.FeeCollector = to.Hex()
			continue
		}
		return ErrUnsafeOrder
	}
	if !seenNet || !seenFee {
		return ErrUnsafeOrder
	}
	v.Admin = admin
	v.Deadline = deadline.Int64()
	return nil
}

func messageJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || len(raw) > 128<<10 {
		return nil, ErrUnsafeOrder
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil, ErrUnsafeOrder
		}
		raw = []byte(s)
	}
	if !json.Valid(raw) {
		return nil, ErrUnsafeOrder
	}
	return raw, nil
}
func quantity(raw json.RawMessage) (*big.Int, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil, ErrUnsafeOrder
	}
	return number(v)
}
func number(v any) (*big.Int, error) {
	var s string
	switch n := v.(type) {
	case string:
		s = n
	case json.Number:
		s = n.String()
	case float64:
		if n < 0 || n > 9007199254740991 || n != float64(uint64(n)) {
			return nil, ErrUnsafeOrder
		}
		s = strconv.FormatUint(uint64(n), 10)
	default:
		return nil, ErrUnsafeOrder
	}
	base := 10
	if strings.HasPrefix(s, "0x") {
		base = 16
		s = s[2:]
	}
	if len(s) == 0 || len(s) > 78 {
		return nil, ErrUnsafeOrder
	}
	n, ok := new(big.Int).SetString(s, base)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return nil, ErrUnsafeOrder
	}
	return n, nil
}
