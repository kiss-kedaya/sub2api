package ledger

import (
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

func recordedTransferFixture() scanner.Transfer {
	return scanner.Transfer{Chain: "bsc", TxHash: "0x" + strings.Repeat("ab", 32), LogIndex: 7,
		From: "0x" + strings.Repeat("cd", 20), To: "0x" + strings.Repeat("ef", 20),
		Token: "0x" + strings.Repeat("12", 20), Amount: big.NewInt(1234567), BlockNumber: 101,
		BlockHash: "0x" + strings.Repeat("34", 32)}
}

func recordedTransferRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"from_address", "to_address", "token", "amount", "block_number", "block_hash", "status"})
}

func TestRecordedTransferMatchesEntireDurableIdentity(t *testing.T) {
	for _, status := range []string{"pending", "credited", "ignored", "reverted"} {
		t.Run(status, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			tr := recordedTransferFixture()
			incoming := tr
			incoming.TxHash = strings.ToUpper(tr.TxHash)
			mock.ExpectQuery(`SELECT from_address, to_address, token, amount, block_number, block_hash, status`).
				WithArgs(tr.Chain, tr.TxHash, tr.LogIndex).
				WillReturnRows(recordedTransferRows().AddRow(strings.ToUpper(tr.From), strings.ToUpper(tr.To),
					strings.ToUpper(tr.Token), "1.234567000000000000", tr.BlockNumber, strings.ToUpper(tr.BlockHash), status))
			known, err := New(db, nil, nil, 0, 0, 0).HasRecordedTransfer(t.Context(), incoming, 6)
			if err != nil || !known {
				t.Fatal("identical durable observation was not recognized", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordedTransferChangesRequireFreshValidation(t *testing.T) {
	for _, mutation := range []string{"sender", "recipient", "token", "amount", "block", "block hash", "unknown status", "decimals"} {
		t.Run(mutation, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			tr := recordedTransferFixture()
			from, to, token, amount, blockHash, status := tr.From, tr.To, tr.Token, "1.234567", tr.BlockHash, "credited"
			block, decimals := tr.BlockNumber, 6
			switch mutation {
			case "sender":
				from = "0x" + strings.Repeat("56", 20)
			case "recipient":
				to = "0x" + strings.Repeat("56", 20)
			case "token":
				token = "0x" + strings.Repeat("56", 20)
			case "amount":
				amount = "1.234568"
			case "block":
				block++
			case "block hash":
				blockHash = "0x" + strings.Repeat("56", 32)
			case "unknown status":
				status = "invalid"
			case "decimals":
				decimals = 18
			}
			mock.ExpectQuery(`SELECT from_address, to_address, token, amount, block_number, block_hash, status`).
				WithArgs(tr.Chain, tr.TxHash, tr.LogIndex).
				WillReturnRows(recordedTransferRows().AddRow(from, to, token, amount, block, blockHash, status))
			known, err := New(db, nil, nil, 0, 0, 0).HasRecordedTransfer(t.Context(), tr, decimals)
			if err != nil || known {
				t.Fatal("changed observation was incorrectly allowed to skip financial verification", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordedTransferMissingOrBrokenEvidenceFailsClosed(t *testing.T) {
	for _, scenario := range []string{"missing", "database unavailable", "invalid stored amount"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			tr := recordedTransferFixture()
			query := mock.ExpectQuery(`SELECT from_address, to_address, token, amount, block_number, block_hash, status`).
				WithArgs(tr.Chain, tr.TxHash, tr.LogIndex)
			failure := errors.New("recorded identity database unavailable")
			switch scenario {
			case "missing":
				query.WillReturnError(sql.ErrNoRows)
			case "database unavailable":
				query.WillReturnError(failure)
			case "invalid stored amount":
				query.WillReturnRows(recordedTransferRows().AddRow(tr.From, tr.To, tr.Token, "invalid", tr.BlockNumber, tr.BlockHash, "pending"))
			}
			known, err := New(db, nil, nil, 0, 0, 0).HasRecordedTransfer(t.Context(), tr, 6)
			if known || (err != nil) != (scenario != "missing") {
				t.Fatal("missing or broken durable evidence was trusted", known, err)
			}
			if scenario == "database unavailable" && !errors.Is(err, failure) {
				t.Fatal("recorded identity database failure was discarded")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecordedTransferInvalidInputNeverSkipsVerification(t *testing.T) {
	for _, mutation := range []string{"nil amount", "zero amount", "negative amount", "negative decimals", "excess decimals", "missing hash"} {
		t.Run(mutation, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			tr, decimals := recordedTransferFixture(), 6
			switch mutation {
			case "nil amount":
				tr.Amount = nil
			case "zero amount":
				tr.Amount = big.NewInt(0)
			case "negative amount":
				tr.Amount = big.NewInt(-1)
			case "negative decimals":
				decimals = -1
			case "excess decimals":
				decimals = 256
			case "missing hash":
				tr.BlockHash = ""
			}
			known, err := New(db, nil, nil, 0, 0, 0).HasRecordedTransfer(t.Context(), tr, decimals)
			if known || err != nil {
				t.Fatal("invalid observation was treated as already validated", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
