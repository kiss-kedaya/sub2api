package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
	"github.com/tyler-smith/go-bip39"
)

// Deriver derives EVM and Solana keys from one BIP39 seed.
// EVM:    m/44'/60'/0'/0/{index}   (secp256k1)
// Solana: m/44'/501'/{index}'/0'   (ed25519, SLIP-0010, all hardened)
type Deriver struct {
	seed []byte
}

// NewDeriverFromSeed wraps an already-derived seed. Used for the hot wallet,
// which must NOT share the deposit seed: one leaked mnemonic would otherwise
// expose both the deposits and the accumulated hot balance.
func NewDeriverFromSeed(seed []byte) *Deriver {
	return &Deriver{seed: seed}
}

// NewDeriver validates the mnemonic and derives the master seed.
func NewDeriver(mnemonic string) (*Deriver, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, fmt.Errorf("invalid BIP39 mnemonic")
	}
	return &Deriver{seed: bip39.NewSeed(mnemonic, "")}, nil
}

// NewMnemonic generates a fresh 24-word mnemonic for initialization.
func NewMnemonic() (string, error) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return "", err
	}
	return bip39.NewMnemonic(entropy)
}

// EVMKey derives the secp256k1 key and address at m/44'/60'/0'/0/{index}.
func (d *Deriver) EVMKey(index uint32) (*ecdsa.PrivateKey, common.Address, error) {
	master, err := hdkeychain.NewMaster(d.seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("master key: %w", err)
	}
	path := []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 60,
		hdkeychain.HardenedKeyStart,
		0,
		index,
	}
	key := master
	for _, step := range path {
		key, err = key.Derive(step)
		if err != nil {
			return nil, common.Address{}, fmt.Errorf("derive %d: %w", step, err)
		}
	}
	priv, err := key.ECPrivKey()
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("ec key: %w", err)
	}
	ecdsaKey := priv.ToECDSA()
	return ecdsaKey, crypto.PubkeyToAddress(ecdsaKey.PublicKey), nil
}

// SolanaKey derives the ed25519 key and address at m/44'/501'/{index}'/0'.
func (d *Deriver) SolanaKey(index uint32) (ed25519.PrivateKey, solana.PublicKey, error) {
	path := []uint32{
		hdkeychain.HardenedKeyStart + 44,
		hdkeychain.HardenedKeyStart + 501,
		hdkeychain.HardenedKeyStart + index,
		hdkeychain.HardenedKeyStart,
	}
	seed, err := slip10Ed25519(d.seed, path)
	if err != nil {
		return nil, solana.PublicKey{}, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, solana.PublicKey{}, fmt.Errorf("ed25519: unexpected public key type")
	}
	return priv, solana.PublicKeyFromBytes(pub), nil
}

// slip10Ed25519 implements SLIP-0010 ed25519 derivation (hardened only).
func slip10Ed25519(seed []byte, path []uint32) ([]byte, error) {
	mac := hmac.New(sha512.New, []byte("ed25519 seed"))
	_, _ = mac.Write(seed)
	sum := mac.Sum(nil)
	key, chainCode := sum[:32], sum[32:]
	for _, index := range path {
		if index < hdkeychain.HardenedKeyStart {
			return nil, fmt.Errorf("SLIP-0010 ed25519 requires hardened indices")
		}
		data := make([]byte, 0, 37)
		data = append(data, 0x00)
		data = append(data, key...)
		var idx [4]byte
		binary.BigEndian.PutUint32(idx[:], index)
		data = append(data, idx[:]...)
		mac = hmac.New(sha512.New, chainCode)
		_, _ = mac.Write(data)
		sum = mac.Sum(nil)
		key, chainCode = sum[:32], sum[32:]
	}
	return key, nil
}
