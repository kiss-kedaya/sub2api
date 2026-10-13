package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// TestManualPrintHotAddresses is a temporary operator helper: it decrypts
// the vault from the database and prints the hot wallet's receive
// addresses, plus the deposit seed's index-0 address as a sanity check.
// Skipped unless HOTADDR_DSN and HOTADDR_PASSPHRASE_FILE are set.
func TestManualPrintHotAddresses(t *testing.T) {
	dsn := os.Getenv("HOTADDR_DSN")
	ppFile := os.Getenv("HOTADDR_PASSPHRASE_FILE")
	if dsn == "" || ppFile == "" {
		t.Skip("set HOTADDR_DSN and HOTADDR_PASSPHRASE_FILE")
	}
	raw, err := os.ReadFile(ppFile)
	if err != nil {
		t.Fatal(err)
	}
	passphrase := strings.TrimSpace(string(raw))

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var vault string
	if err := db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'crypto.signer.vault'`).Scan(&vault); err != nil {
		t.Fatal(err)
	}
	contents, err := openVaultString(vault, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	deposit, err := NewDeriver(contents.Deposit)
	if err != nil {
		t.Fatal(err)
	}
	hot, err := NewDeriver(contents.Hot)
	if err != nil {
		t.Fatal(err)
	}
	_, deposit0, err := deposit.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	_, hotEVM, err := hot.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	_, hotSol, err := hot.SolanaKey(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("deposit[0].evm = %s", deposit0.Hex())
	t.Logf("hot[0].evm     = %s", hotEVM.Hex())
	t.Logf("hot[0].sol     = %s", hotSol.String())
}
