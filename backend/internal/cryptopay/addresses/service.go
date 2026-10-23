package addresses

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"strings"
	"sync"
)

// Deriver supplies public deposit addresses from the unlocked local wallet.
type Deriver interface {
	Derive(ctx context.Context, start, count uint32) ([]Derived, error)
}

// PoolStore is the persistence surface the service needs; *Repo implements
// it and tests substitute an in-memory fake.
type PoolStore interface {
	MaxDerivationIndex(ctx context.Context) (int64, error)
	InsertPoolBatch(ctx context.Context, family string, start int64, addresses []string) error
	FreePoolCount(ctx context.Context, family string) (int64, error)
	ExistingAssignment(ctx context.Context, userID int64, family string) (*Assignment, error)
	AssignFromPool(ctx context.Context, userID int64, family string) (*Assignment, error)
	ListByUser(ctx context.Context, userID int64) ([]Assignment, error)
	SetStatus(ctx context.Context, id int64, status string) error
	ActiveAddresses(ctx context.Context, family string) ([]string, error)
	LookupByFamily(ctx context.Context, family, address string) (*Assignment, error)
	AdminList(ctx context.Context, userID int64, family string, limit, offset int, sortBy, sortDir string) ([]AdminRow, int64, error)
	AdminSummary(ctx context.Context, userID int64, family string) (*AdminSummary, error)
}

// Service coordinates pool top-up and lazy assignment.
type Service struct {
	poolMu             sync.Mutex // serialize top-up and deriver replacement
	guardMu            sync.RWMutex
	beforeAssign       func(context.Context) error
	validateAssignment func(context.Context, Assignment) error
	repo               PoolStore
	deriver            Deriver
	topUpBatch         int
	lowWatermark       int64
}

// Guards are attached by the wallet facade, including for the direct address
// HTTP endpoint. Every assignment is checked before its public address leaves.
func (s *Service) SetSecurityGuards(before func(context.Context) error, validate func(context.Context, Assignment) error) {
	s.guardMu.Lock()
	defer s.guardMu.Unlock()
	s.beforeAssign, s.validateAssignment = before, validate
}

// NewService wires the address service. topUpBatch is derived per request;
// lowWatermark triggers background top-up (Phase 2 keeps it simple: top up
// synchronously when a request finds the pool empty).
func NewService(repo PoolStore, deriver Deriver) *Service {
	return &Service{
		repo:         repo,
		deriver:      deriver,
		topUpBatch:   50,
		lowWatermark: 10,
	}
}

// GetOrAssign returns the user's addresses, assigning from the pool (with a
// synchronous top-up when empty) on first request. Idempotent.
func (s *Service) GetOrAssign(ctx context.Context, userID int64) (map[string]string, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	s.guardMu.RLock()
	before, validate := s.beforeAssign, s.validateAssignment
	s.guardMu.RUnlock()
	if before != nil {
		if err := before(ctx); err != nil {
			return nil, err
		}
	}
	out := map[string]string{}
	for _, family := range []string{FamilyEVM, FamilySolana} {
		existing, err := s.repo.ExistingAssignment(ctx, userID, family)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if validate != nil {
				if err := validate(ctx, *existing); err != nil {
					return nil, err
				}
			}
			// A disabled address must not be handed out again: the scanner
			// stops watching it, so a deposit would vanish without a record.
			if existing.Status != "active" {
				return nil, fmt.Errorf("your %s deposit address is disabled; contact the operator", family)
			}
			out[family] = existing.Address
			continue
		}
		assignment, err := s.repo.AssignFromPool(ctx, userID, family)
		if err != nil {
			return nil, err
		}
		if assignment == nil {
			if err := s.topUp(ctx); err != nil {
				return nil, fmt.Errorf("top up pool: %w", err)
			}
			assignment, err = s.repo.AssignFromPool(ctx, userID, family)
			if err != nil {
				return nil, err
			}
			if assignment == nil {
				return nil, fmt.Errorf("address pool exhausted for %s", family)
			}
		}
		if validate != nil {
			if err := validate(ctx, *assignment); err != nil {
				return nil, err
			}
		}
		out[family] = assignment.Address
	}
	return out, nil
}

// topUp derives one batch starting after the current maximum index and
// stores it for both families.
func (s *Service) topUp(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	if s.deriver == nil {
		return fmt.Errorf("wallet is not initialized")
	}
	maxIndex, err := s.repo.MaxDerivationIndex(ctx)
	if err != nil {
		return err
	}
	start := uint32(maxIndex + 1)
	// Index 0 is the hot/gas wallet and must never be handed to a user:
	// sweeping an address to itself is a no-op and its balance would mix
	// platform funds with a user's deposit.
	if maxIndex < 0 {
		start = hotWalletIndex + 1
	}
	derived, err := s.deriver.Derive(ctx, start, uint32(s.topUpBatch))
	if err != nil {
		return err
	}
	if len(derived) != s.topUpBatch {
		return fmt.Errorf("deriver returned %d addresses, expected %d", len(derived), s.topUpBatch)
	}
	evm := make([]string, 0, len(derived))
	solana := make([]string, 0, len(derived))
	for i, d := range derived {
		if d.Index != int64(start)+int64(i) || strings.TrimSpace(d.EVM) == "" || strings.TrimSpace(d.Solana) == "" {
			return fmt.Errorf("deriver returned an invalid address/index at offset %d", i)
		}
		evm = append(evm, d.EVM)
		solana = append(solana, d.Solana)
	}
	if err := s.repo.InsertPoolBatch(ctx, FamilyEVM, int64(start), evm); err != nil {
		return err
	}
	return s.repo.InsertPoolBatch(ctx, FamilySolana, int64(start), solana)
}

// SetDeriver replaces the derivation client only after the caller has checked
// that the replacement derives the existing pool's seed. No old top-up can
// publish a batch after this switch.
func (s *Service) SetDeriver(deriver Deriver) {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	s.deriver = deriver
}

// hotWalletIndex is the HD index reserved for the hot/gas wallet. It must
// match the built-in Gas wallet index (zero).
const hotWalletIndex = 0

// EnsurePool tops the pool up when below the low watermark. Called by the
// background worker; safe to call repeatedly.
func (s *Service) EnsurePool(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	for _, family := range []string{FamilyEVM, FamilySolana} {
		count, err := s.repo.FreePoolCount(ctx, family)
		if err != nil {
			return err
		}
		if count < s.lowWatermark {
			if err := s.topUp(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListByUser exposes the user's assignments to the HTTP layer.
func (s *Service) ListByUser(ctx context.Context, userID int64) ([]Assignment, error) {
	return s.repo.ListByUser(ctx, userID)
}

// PoolStatus reports how many unassigned addresses remain per family. An
// empty pool means new users cannot be given a deposit address at all, so the
// console surfaces it before it runs dry.
type PoolStatus struct {
	Free         map[string]int64 `json:"free"`
	LowWatermark int64            `json:"low_watermark"`
}

func (s *Service) PoolStatus(ctx context.Context) (*PoolStatus, error) {
	out := &PoolStatus{Free: map[string]int64{}, LowWatermark: s.lowWatermark}
	for _, family := range []string{FamilyEVM, FamilySolana} {
		count, err := s.repo.FreePoolCount(ctx, family)
		if err != nil {
			return nil, err
		}
		out.Free[family] = count
	}
	return out, nil
}

// SetStatus exposes assignment status changes to the admin layer.
func (s *Service) SetStatus(ctx context.Context, id int64, status string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	return s.repo.SetStatus(ctx, id, status)
}

// ActiveAddresses returns every active address for a chain family (the
// scanners' watch list).
func (s *Service) ActiveAddresses(ctx context.Context, family string) ([]string, error) {
	return s.repo.ActiveAddresses(ctx, family)
}

// LookupAny resolves an address regardless of status so incoming funds are
// never dropped: unknown-to-us addresses return nil.
func (s *Service) LookupAny(ctx context.Context, family, address string) (*Assignment, error) {
	return s.repo.LookupByFamily(ctx, family, address)
}

// AdminList exposes the aggregated address list to the admin console.
func (s *Service) AdminList(ctx context.Context, userID int64, family string, limit, offset int, sortBy, sortDir string) ([]AdminRow, int64, error) {
	return s.repo.AdminList(ctx, userID, family, limit, offset, sortBy, sortDir)
}

// AdminSummary exposes the whole-set totals for the console header.
func (s *Service) AdminSummary(ctx context.Context, userID int64, family string) (*AdminSummary, error) {
	return s.repo.AdminSummary(ctx, userID, family)
}
