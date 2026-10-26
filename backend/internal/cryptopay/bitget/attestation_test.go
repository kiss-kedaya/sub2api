package bitget

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func attestFixture(t *testing.T, o *Order, in Intent) (*Client, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	c := &Client{key: &key.PublicKey}
	attachFixture(t, key, o, in)
	return c, key
}
func attachFixture(t *testing.T, key *rsa.PrivateKey, o *Order, in Intent) {
	t.Helper()
	o.Attestation = nil
	req := Request{Chain: o.Chain, Contract: in.Token, From: in.From, To: in.To, Amount: o.Amount, NoGas: true, PayToken: in.Token}
	rawReq, err := json.MarshalIndent(req, "", " ")
	require.NoError(t, err)
	rawRes, err := json.MarshalIndent(struct {
		Status int    `json:"status"`
		Data   *Order `json:"data"`
	}{0, o}, "", "  ")
	require.NoError(t, err)
	sign := func(b []byte) string {
		h := sha256.Sum256(b)
		s, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
		require.NoError(t, e)
		return "0x" + hex.EncodeToString(s)
	}
	a, b := sign(rawRes), sign(rawReq)
	o.Attestation = &Attestation{Request: rawReq, Response: rawRes, ResponseSignature: a, RequestSignature: b, DoubleSignature: sign([]byte(a + b))}
}

func TestAttestedOpaqueOrderSurvivesStorageAndSignsOnlyApprovedDigests(t *testing.T) {
	o, in := fixtureOrder()
	c, _ := attestFixture(t, o, in)
	data, err := json.Marshal(o)
	require.NoError(t, err)
	// JSONB is free to rearrange the outer object. Original signed request
	// and response bytes must remain byte-for-byte intact inside base64 fields.
	var obj map[string]any
	require.NoError(t, json.Unmarshal(data, &obj))
	stored, err := json.Marshal(obj)
	require.NoError(t, err)
	var restored Order
	require.NoError(t, json.Unmarshal(stored, &restored))
	v, err := c.ValidateSigning(&restored, in, time.Now())
	require.NoError(t, err)
	require.Len(t, v.Digests, 1)
	key, err := gethcrypto.GenerateKey()
	require.NoError(t, err)
	sig, err := gethcrypto.Sign(v.Digests[0], key)
	require.NoError(t, err)
	pub, err := gethcrypto.SigToPub(v.Digests[0], sig)
	require.NoError(t, err)
	require.Equal(t, gethcrypto.PubkeyToAddress(key.PublicKey), gethcrypto.PubkeyToAddress(*pub))
	sig[64] += 27
	require.Contains(t, []byte{27, 28}, sig[64])
	// A persisted edit to the destination, amount, hash or request is rejected.
	for _, mutate := range []func(*Order){
		func(x *Order) { x.To = x.From }, func(x *Order) { x.Amount = "9.96" },
		func(x *Order) { x.Source.EVM.Messages[0].Hash = "0x" + strings.Repeat("cd", 32) },
		func(x *Order) { x.Attestation.Request[0] = '[' },
	} {
		var copy Order
		require.NoError(t, json.Unmarshal(stored, &copy))
		mutate(&copy)
		_, err = c.ValidateSigning(&copy, in, time.Now())
		require.Error(t, err)
	}
}

func TestAttestedOrderRejectsAuthOverrideAndUnexpectedMessageSequences(t *testing.T) {
	for _, name := range []string{"duplicate call", "foreign override", "unknown type", "missing auth", "valid auth and call"} {
		t.Run(name, func(t *testing.T) {
			o, in := fixtureOrder()
			o.NoGas.NeedAuth = true
			o.Source.EVM.Messages = append([]Message{{Type: "auth", Hash: "0x" + strings.Repeat("ab", 32)}}, o.Source.EVM.Messages...)
			switch name {
			case "duplicate call":
				o.Source.EVM.Messages[0].Type = "call"
			case "foreign override":
				in.CurrentDelegate = in.To
			case "unknown type":
				o.Source.EVM.Messages[0].Type = "permit"
			case "missing auth":
				o.Source.EVM.Messages = o.Source.EVM.Messages[1:]
			}
			c, _ := attestFixture(t, o, in)
			_, err := c.ValidateSigning(o, in, time.Now())
			if name == "valid auth and call" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
