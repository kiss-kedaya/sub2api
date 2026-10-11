// Package addresses manages the HD-derived deposit address pool and the
// per-user assignment of addresses.
//
// Assignments are stored per chain FAMILY ("evm" | "solana"): one EVM
// address serves bsc/polygon/plasma, one Solana address serves solana.
package addresses

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Family constants stored in crypto_address_pool.chain_family and
// deposit_addresses.chain.
const (
	FamilyEVM    = "evm"
	FamilySolana = "solana"
)

// FamilyForChain maps a chain id to its family.
func FamilyForChain(chain string) (string, error) {
	switch chain {
	case "bsc", "polygon", "plasma":
		return FamilyEVM, nil
	case "solana":
		return FamilySolana, nil
	default:
		return "", fmt.Errorf("unsupported chain %q", chain)
	}
}

// Assignment is one row of deposit_addresses.
type Assignment struct {
	ID              int64
	UserID          int64
	Family          string
	Address         string
	DerivationIndex int64
	Status          string
}

// Derived is one derived address pair returned by the signer.
type Derived struct {
	Index  int64  `json:"index"`
	EVM    string `json:"evm"`
	Solana string `json:"solana"`
}

// Repo provides pool and assignment persistence.
type Repo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) *Repo {
	return &Repo{db: db}
}

// MaxDerivationIndex returns the highest derivation index in the pool, or -1
// when empty. The next derivation batch starts after it.
func (r *Repo) MaxDerivationIndex(ctx context.Context) (int64, error) {
	var maxIndex sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		`SELECT MAX(derivation_index) FROM crypto_address_pool`).Scan(&maxIndex)
	if err != nil {
		return 0, fmt.Errorf("max derivation index: %w", err)
	}
	if !maxIndex.Valid {
		return -1, nil
	}
	return maxIndex.Int64, nil
}

// InsertPoolBatch inserts derived addresses for one family; existing
// (family, index) rows are skipped so re-running a batch is safe.
func (r *Repo) InsertPoolBatch(ctx context.Context, family string, start int64, addresses []string) error {
	for i, address := range addresses {
		index := start + int64(i)
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO crypto_address_pool (chain_family, derivation_index, address, status)
			 VALUES ($1, $2, $3, 'free')
			 ON CONFLICT (chain_family, derivation_index) DO NOTHING`,
			family, index, address)
		if err != nil {
			return fmt.Errorf("insert pool row %d: %w", index, err)
		}
	}
	return nil
}

// FreePoolCount counts unassigned addresses for a family.
func (r *Repo) FreePoolCount(ctx context.Context, family string) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM crypto_address_pool WHERE chain_family = $1 AND status = 'free'`,
		family).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("free pool count: %w", err)
	}
	return count, nil
}

// ExistingAssignment returns the user's current assignment for a family, or
// nil when none exists.
func (r *Repo) ExistingAssignment(ctx context.Context, userID int64, family string) (*Assignment, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, chain, address, derivation_index, status
		   FROM deposit_addresses WHERE user_id = $1 AND chain = $2`,
		userID, family)
	var a Assignment
	err := row.Scan(&a.ID, &a.UserID, &a.Family, &a.Address, &a.DerivationIndex, &a.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("existing assignment: %w", err)
	}
	return &a, nil
}

// AssignFromPool atomically takes one free address for the family and binds
// it to the user. Returns nil when the pool is empty (caller tops up and
// retries). Safe under concurrent first-requests: a lost race returns the
// winner's row via the unique (user_id, chain) index.
func (r *Repo) AssignFromPool(ctx context.Context, userID int64, family string) (*Assignment, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin assign tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var poolID, index int64
	var address string
	err = tx.QueryRowContext(ctx,
		`SELECT id, derivation_index, address
		   FROM crypto_address_pool
		  WHERE chain_family = $1 AND status = 'free'
		  ORDER BY derivation_index
		  FOR UPDATE SKIP LOCKED
		  LIMIT 1`, family).Scan(&poolID, &index, &address)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select free address: %w", err)
	}

	var assignmentID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO deposit_addresses (user_id, chain, address, derivation_index, status)
		 VALUES ($1, $2, $3, $4, 'active')
		 ON CONFLICT (user_id, chain) DO NOTHING
		 RETURNING id`, userID, family, address, index).Scan(&assignmentID)
	if errors.Is(err, sql.ErrNoRows) {
		// Lost a concurrent race; return the winner's assignment.
		_ = tx.Rollback()
		return r.ExistingAssignment(ctx, userID, family)
	}
	if err != nil {
		return nil, fmt.Errorf("insert assignment: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE crypto_address_pool
		    SET status = 'assigned', assigned_user_id = $1, assigned_at = NOW()
		  WHERE id = $2`, userID, poolID); err != nil {
		return nil, fmt.Errorf("mark pool assigned: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit assign tx: %w", err)
	}
	return &Assignment{
		ID: assignmentID, UserID: userID, Family: family,
		Address: address, DerivationIndex: index, Status: "active",
	}, nil
}

// ListByUser returns all assignments for a user.
func (r *Repo) ListByUser(ctx context.Context, userID int64) ([]Assignment, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, chain, address, derivation_index, status
		   FROM deposit_addresses WHERE user_id = $1 ORDER BY chain`, userID)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Assignment
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.ID, &a.UserID, &a.Family, &a.Address, &a.DerivationIndex, &a.Status); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// LookupByAddress maps an on-chain address back to its owner. chain selects
// the family; EVM comparisons are case-insensitive.
func (r *Repo) LookupByAddress(ctx context.Context, chain, address string) (*Assignment, error) {
	family, err := FamilyForChain(chain)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, chain, address, derivation_index, status
		   FROM deposit_addresses
		  WHERE chain = $1 AND lower(address) = lower($2)`, family, address)
	var a Assignment
	err = row.Scan(&a.ID, &a.UserID, &a.Family, &a.Address, &a.DerivationIndex, &a.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("address lookup: %w", err)
	}
	return &a, nil
}

// ActiveAddresses returns every active address for a family, used by the
// scanners' batch filter.
func (r *Repo) ActiveAddresses(ctx context.Context, family string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT address FROM deposit_addresses WHERE chain = $1 AND status = 'active'`, family)
	if err != nil {
		return nil, fmt.Errorf("list active addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	return out, rows.Err()
}

// SetStatus updates an assignment's status (active/disabled).
func (r *Repo) SetStatus(ctx context.Context, id int64, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("invalid status %q", status)
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE deposit_addresses SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("set assignment status: %w", err)
	}
	return nil
}

// CountAssignments returns totals for the admin dashboard.
func (r *Repo) CountAssignments(ctx context.Context) (total, active int64, err error) {
	err = r.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE status = 'active')
		   FROM deposit_addresses`).Scan(&total, &active)
	if err != nil {
		return 0, 0, fmt.Errorf("count assignments: %w", err)
	}
	return total, active, nil
}
