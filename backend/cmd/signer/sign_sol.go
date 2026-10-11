package main

import (
	"encoding/base64"
	"fmt"
	"math/big"

	"github.com/gagliardetto/solana-go"
	associatedtokenaccount "github.com/gagliardetto/solana-go/programs/associated-token-account"
	"github.com/gagliardetto/solana-go/programs/system"
	"github.com/gagliardetto/solana-go/programs/token"
)

// solSignRequest mirrors the /sign/sol request body.
type solSignRequest struct {
	Index           uint32 `json:"index"`
	Mint            string `json:"mint"` // SPL mint; empty = native SOL transfer
	To              string `json:"to"`
	Amount          string `json:"amount"` // raw units, decimal string
	RecentBlockhash string `json:"recent_blockhash"`
}

// signSolTransfer builds a transfer of SPL tokens (or native SOL) from the
// derived address to the policy target.
func signSolTransfer(d *Deriver, req solSignRequest) (string, error) {
	priv, from, err := d.SolanaKey(req.Index)
	if err != nil {
		return "", err
	}
	to, err := solana.PublicKeyFromBase58(req.To)
	if err != nil {
		return "", fmt.Errorf("invalid to address: %w", err)
	}
	amount, ok := new(big.Int).SetString(req.Amount, 10)
	if !ok || amount.Sign() <= 0 {
		return "", fmt.Errorf("invalid amount")
	}
	blockhash, err := solana.HashFromBase58(req.RecentBlockhash)
	if err != nil {
		return "", fmt.Errorf("invalid recent_blockhash: %w", err)
	}

	builder := solana.NewTransactionBuilder()
	if req.Mint == "" || req.Mint == "native" {
		builder = builder.AddInstruction(system.NewTransferInstruction(
			amount.Uint64(), from, to,
		).Build())
	} else {
		mint, err := solana.PublicKeyFromBase58(req.Mint)
		if err != nil {
			return "", fmt.Errorf("invalid mint: %w", err)
		}
		sourceATA, _, err := solana.FindAssociatedTokenAddress(from, mint)
		if err != nil {
			return "", fmt.Errorf("source ATA: %w", err)
		}
		destATA, _, err := solana.FindAssociatedTokenAddress(to, mint)
		if err != nil {
			return "", fmt.Errorf("dest ATA: %w", err)
		}
		// Ensure the destination ATA exists (idempotent; hot wallet ATA is
		// normally already created, the instruction is cheap insurance).
		builder = builder.AddInstruction(associatedtokenaccount.NewCreateIdempotentInstruction(
			from, to, mint,
		).Build())
		builder = builder.AddInstruction(token.NewTransferCheckedInstruction(
			amount.Uint64(), 6, mint, sourceATA, destATA, from, nil,
		).Build())
	}

	tx, err := builder.SetRecentBlockHash(blockhash).Build()
	if err != nil {
		return "", fmt.Errorf("build tx: %w", err)
	}
	solPriv := solana.PrivateKey(priv)
	if _, err := tx.Sign(func(key solana.PublicKey) *solana.PrivateKey {
		if key.Equals(from) {
			return &solPriv
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("sign tx: %w", err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
