package cryptopay

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

// MaxOwnerLookupAddresses bounds one address-owners request after
// de-duplication. The pool holds a few thousand rows; this keeps a single
// request from turning into a bulk export of user identities.
const MaxOwnerLookupAddresses = 100

var (
	// ErrTooManyOwnerAddresses is returned when more than MaxOwnerLookupAddresses
	// distinct valid addresses are requested.
	ErrTooManyOwnerAddresses = errors.New("too many addresses")
	// ErrOwnerLookupFailed is the only error this lookup returns for a database
	// failure. database/sql puts the offending column value into its scan
	// errors and these rows hold user emails, so no cause is ever wrapped.
	ErrOwnerLookupFailed = errors.New("address owner lookup failed")
)

var evmAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// AddressOwner is the user a deposit address was assigned to. Username and
// Email are personal data: they belong in admin API responses only, never in
// logs or error text.
type AddressOwner struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	// Deleted is true when the user is soft-deleted or no longer exists. The
	// address stays attributed to the id so history does not lose its owner.
	Deleted bool `json:"deleted"`
}

// normalizeOwnerAddresses trims, validates (EVM 0x + 40 hex), lower-cases and
// de-duplicates, keeping first-seen order. Invalid values are dropped.
func normalizeOwnerAddresses(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if !evmAddressPattern.MatchString(value) {
			continue
		}
		value = strings.ToLower(value)
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// The pool stores checksummed addresses while sweep records and deposits keep
// them lower-case, so the comparison lower-cases the stored side. The pool is
// a few thousand rows; no functional index is warranted for this read.
//
// LEFT JOIN keeps soft-deleted users (deleted_at is not filtered) and also
// rows whose user was hard-deleted; both report deleted = true.
const addressOwnersQuery = `SELECT lower(p.address), p.assigned_user_id,
	       COALESCE(u.username, ''), COALESCE(u.email, ''),
	       (u.id IS NULL OR u.deleted_at IS NOT NULL)
	  FROM crypto_address_pool p
	  LEFT JOIN users u ON u.id = p.assigned_user_id
	 WHERE lower(p.address) = ANY($1) AND p.assigned_user_id IS NOT NULL
	 ORDER BY p.id`

// LookupAddressOwners maps deposit addresses to their assigned users. The
// result is keyed by lower-case address; addresses with no owner are absent.
func (s *Service) LookupAddressOwners(ctx context.Context, addresses []string) (map[string]AddressOwner, error) {
	normalized := normalizeOwnerAddresses(addresses)
	if len(normalized) > MaxOwnerLookupAddresses {
		return nil, ErrTooManyOwnerAddresses
	}
	owners := make(map[string]AddressOwner, len(normalized))
	if len(normalized) == 0 {
		return owners, nil
	}
	if s == nil || s.db == nil {
		return nil, ErrOwnerLookupFailed
	}
	rows, err := s.db.QueryContext(ctx, addressOwnersQuery, pq.Array(normalized))
	if err != nil {
		return nil, ErrOwnerLookupFailed
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var address string
		var owner AddressOwner
		if err := rows.Scan(&address, &owner.UserID, &owner.Username, &owner.Email, &owner.Deleted); err != nil {
			return nil, ErrOwnerLookupFailed
		}
		address = strings.ToLower(address)
		if _, exists := owners[address]; !exists {
			owners[address] = owner
		}
	}
	if err := rows.Err(); err != nil {
		return nil, ErrOwnerLookupFailed
	}
	return owners, nil
}
