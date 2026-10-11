package addresses

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Deriver is the signer surface the service needs; *SignerClient implements
// it and tests substitute a fake.
type Deriver interface {
	Derive(ctx context.Context, start, count uint32) ([]Derived, error)
}

// SignerClient talks to the isolated signing service over its unix socket.
type SignerClient struct {
	http *http.Client
}

// NewSignerClient builds a client bound to the signer socket path.
func NewSignerClient(socketPath string) *SignerClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &SignerClient{http: &http.Client{Transport: transport, Timeout: 30 * time.Second}}
}

// Derive asks the signer for a batch of derived address pairs.
func (c *SignerClient) Derive(ctx context.Context, start, count uint32) ([]Derived, error) {
	body, err := json.Marshal(map[string]uint32{"start": start, "count": count})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://signer/derive", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("signer derive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signer derive: status %d", resp.StatusCode)
	}
	var out struct {
		Addresses []Derived `json:"addresses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("signer derive decode: %w", err)
	}
	return out.Addresses, nil
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
}

// Service coordinates pool top-up and lazy assignment.
type Service struct {
	repo         PoolStore
	signer       Deriver
	topUpBatch   int
	lowWatermark int64
}

// NewService wires the address service. topUpBatch is derived per request;
// lowWatermark triggers background top-up (Phase 2 keeps it simple: top up
// synchronously when a request finds the pool empty).
func NewService(repo PoolStore, signer Deriver) *Service {
	return &Service{
		repo:         repo,
		signer:       signer,
		topUpBatch:   50,
		lowWatermark: 10,
	}
}

// GetOrAssign returns the user's addresses, assigning from the pool (with a
// synchronous top-up when empty) on first request. Idempotent.
func (s *Service) GetOrAssign(ctx context.Context, userID int64) (map[string]string, error) {
	out := map[string]string{}
	for _, family := range []string{FamilyEVM, FamilySolana} {
		existing, err := s.repo.ExistingAssignment(ctx, userID, family)
		if err != nil {
			return nil, err
		}
		if existing != nil {
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
		out[family] = assignment.Address
	}
	return out, nil
}

// topUp derives one batch starting after the current maximum index and
// stores it for both families.
func (s *Service) topUp(ctx context.Context) error {
	maxIndex, err := s.repo.MaxDerivationIndex(ctx)
	if err != nil {
		return err
	}
	start := uint32(maxIndex + 1)
	derived, err := s.signer.Derive(ctx, start, uint32(s.topUpBatch))
	if err != nil {
		return err
	}
	evm := make([]string, 0, len(derived))
	solana := make([]string, 0, len(derived))
	for _, d := range derived {
		evm = append(evm, d.EVM)
		solana = append(solana, d.Solana)
	}
	if err := s.repo.InsertPoolBatch(ctx, FamilyEVM, int64(start), evm); err != nil {
		return err
	}
	return s.repo.InsertPoolBatch(ctx, FamilySolana, int64(start), solana)
}

// EnsurePool tops the pool up when below the low watermark. Called by the
// background worker; safe to call repeatedly.
func (s *Service) EnsurePool(ctx context.Context) error {
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

// SetStatus exposes assignment status changes to the admin layer.
func (s *Service) SetStatus(ctx context.Context, id int64, status string) error {
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
