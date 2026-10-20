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
	if req.Index >= 1<<31 {
		return "", fmt.Errorf("invalid derivation index")
	}
	priv, from, err := d.SolanaKey(req.Index)
	if err != nil {
		return "", err
	}
	to, err := solana.PublicKeyFromBase58(req.To)
	if err != nil {
		return "", fmt.Errorf("invalid to address: %w", err)
	}
	amount, err := positiveUint(req.Amount, 64)
	if err != nil {
		return "", fmt.Errorf("invalid uint64 amount")
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
			amount.Uint64(), 6, sourceATA, mint, destATA, from, nil,
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

func solBudgetCharges(p *Policy, req solSignRequest) (map[string]*big.Int, error) {
	if p.Solana == nil || p.Solana.MaxFeeLamports == 0 {
		return nil, fmt.Errorf("solana_not_allowed")
	}
	amount, err := positiveUint(req.Amount, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid uint64 amount")
	}
	fee := new(big.Int).SetUint64(p.Solana.MaxFeeLamports)
	charges := map[string]*big.Int{}
	if req.Mint == "" || req.Mint == "native" {
		fee.Add(fee, amount)
	} else {
		if _, err := solana.PublicKeyFromBase58(req.Mint); err != nil {
			return nil, fmt.Errorf("invalid mint")
		}
		if p.Solana.ATARentLamports == 0 {
			return nil, fmt.Errorf("solana ATA rent cap required")
		}
		fee.Add(fee, new(big.Int).SetUint64(p.Solana.ATARentLamports))
		charges[p.limitKey("solana", req.Mint)] = amount
	}
	if !fee.IsUint64() {
		return nil, fmt.Errorf("uint64 total cost overflow")
	}
	charges[p.limitKey("solana", "native")] = fee
	return charges, nil
}
