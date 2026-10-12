package addresses

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// fakeStore is an in-memory PoolStore with the same semantics as the SQL
// repo: unique (user, family) assignment, free rows consumed in index order.
type fakeStore struct {
	mu          sync.Mutex
	nextIndex   int64
	free        map[string][]int64 // family -> pool row ids
	rows        map[int64]poolRow  // pool row id -> row
	assignments map[string]Assignment
}

type poolRow struct {
	id      int64
	index   int64
	family  string
	address string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		free:        map[string][]int64{},
		rows:        map[int64]poolRow{},
		assignments: map[string]Assignment{},
	}
}

func (f *fakeStore) MaxDerivationIndex(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextIndex - 1, nil
}

func (f *fakeStore) InsertPoolBatch(_ context.Context, family string, start int64, addresses []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, address := range addresses {
		id := int64(len(f.rows) + 1)
		f.rows[id] = poolRow{id: id, index: start + int64(i), family: family, address: address}
		f.free[family] = append(f.free[family], id)
	}
	if end := start + int64(len(addresses)); end > f.nextIndex {
		f.nextIndex = end
	}
	return nil
}

func (f *fakeStore) FreePoolCount(_ context.Context, family string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.free[family])), nil
}

func (f *fakeStore) ExistingAssignment(_ context.Context, userID int64, family string) (*Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%d/%s", userID, family)
	if a, ok := f.assignments[key]; ok {
		copied := a
		return &copied, nil
	}
	return nil, nil
}

func (f *fakeStore) AssignFromPool(_ context.Context, userID int64, family string) (*Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := fmt.Sprintf("%d/%s", userID, family)
	if a, ok := f.assignments[key]; ok {
		copied := a
		return &copied, nil
	}
	if len(f.free[family]) == 0 {
		return nil, nil
	}
	rowID := f.free[family][0]
	f.free[family] = f.free[family][1:]
	row := f.rows[rowID]
	a := Assignment{ID: rowID, UserID: userID, Family: family, Address: row.address, DerivationIndex: row.index, Status: "active"}
	f.assignments[key] = a
	copied := a
	return &copied, nil
}

func (f *fakeStore) ListByUser(_ context.Context, userID int64) ([]Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Assignment
	for _, a := range f.assignments {
		if a.UserID == userID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeStore) SetStatus(_ context.Context, id int64, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, a := range f.assignments {
		if a.ID == id {
			a.Status = status
			f.assignments[key] = a
			return nil
		}
	}
	return nil
}

func (f *fakeStore) ActiveAddresses(_ context.Context, family string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.assignments {
		if a.Family == family && a.Status == "active" {
			out = append(out, a.Address)
		}
	}
	return out, nil
}

func (f *fakeStore) LookupByFamily(_ context.Context, family, address string) (*Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.assignments {
		if a.Family == family && a.Address == address {
			copied := a
			return &copied, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) AdminList(_ context.Context, userID int64, family string, limit, offset int) ([]AdminRow, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []AdminRow
	for _, a := range f.assignments {
		if userID > 0 && a.UserID != userID {
			continue
		}
		if family != "" && a.Family != family {
			continue
		}
		out = append(out, AdminRow{ID: a.ID, UserID: a.UserID, Family: a.Family, Address: a.Address, Status: a.Status})
	}
	return out, int64(len(out)), nil
}

// signer process.
type fakeSigner struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeSigner) Derive(_ context.Context, start, count uint32) ([]Derived, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := make([]Derived, 0, count)
	for i := uint32(0); i < count; i++ {
		index := start + i
		out = append(out, Derived{
			Index:  int64(index),
			EVM:    fmt.Sprintf("0xevm%08d", index),
			Solana: fmt.Sprintf("sol%08d", index),
		})
	}
	return out, nil
}

func TestGetOrAssignIsIdempotent(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, &fakeSigner{})
	ctx := context.Background()

	first, err := svc.GetOrAssign(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.GetOrAssign(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if first[FamilyEVM] != second[FamilyEVM] || first[FamilySolana] != second[FamilySolana] {
		t.Fatalf("assignment not idempotent: %v vs %v", first, second)
	}
}

// The hot/gas wallet lives at index 0. It must never be assigned to a user:
// sweeping an address to itself is a no-op and the child's credited balance
// would mix with platform funds.
func TestFirstTopUpSkipsHotWalletIndex(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, &fakeSigner{})
	ctx := context.Background()

	if _, err := svc.GetOrAssign(ctx, 7); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{FamilyEVM, FamilySolana} {
		for id, row := range store.rows {
			if row.family != family {
				continue
			}
			if row.index == hotWalletIndex {
				t.Fatalf("%s pool row %d reuses the hot wallet index %d", family, id, hotWalletIndex)
			}
		}
	}
}

func TestGetOrAssignTopsUpEmptyPool(t *testing.T) {
	store := newFakeStore()
	signer := &fakeSigner{}
	svc := NewService(store, signer)
	ctx := context.Background()

	out, err := svc.GetOrAssign(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if out[FamilyEVM] == "" || out[FamilySolana] == "" {
		t.Fatalf("missing addresses: %v", out)
	}
	if signer.calls == 0 {
		t.Fatal("expected signer top-up call")
	}
}

func TestGetOrAssignConcurrentSameUser(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, &fakeSigner{})
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]map[string]string, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = svc.GetOrAssign(ctx, 99)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if results[i][FamilyEVM] != results[0][FamilyEVM] {
			t.Fatalf("concurrent calls returned different EVM addresses: %v vs %v", results[i], results[0])
		}
	}
}

func TestEnsurePoolTopsUpBelowWatermark(t *testing.T) {
	store := newFakeStore()
	signer := &fakeSigner{}
	svc := NewService(store, signer)
	ctx := context.Background()

	if err := svc.EnsurePool(ctx); err != nil {
		t.Fatal(err)
	}
	evm, err := store.FreePoolCount(ctx, FamilyEVM)
	if err != nil {
		t.Fatal(err)
	}
	if evm < svc.lowWatermark {
		t.Fatalf("pool not topped up: %d", evm)
	}
}
