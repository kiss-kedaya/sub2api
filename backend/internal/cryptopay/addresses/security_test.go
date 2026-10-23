package addresses

import (
	"context"
	"errors"
	"testing"
)

func TestSecurityGuardPreventsAssignmentBeforePoolMutation(t *testing.T) {
	store := newFakeStore()
	svc := NewService(store, &fakeDeriver{})
	incident := errors.New("wallet locked")
	svc.SetSecurityGuards(func(context.Context) error { return incident }, nil)
	if got, err := svc.GetOrAssign(t.Context(), 101); got != nil || !errors.Is(err, incident) {
		t.Fatal("assignment bypassed lock", got, err)
	}
	if len(store.assignments) != 0 || len(store.rows) != 0 {
		t.Fatal("locked request changed address ownership")
	}
}

func TestSecurityValidatesExistingAndNewAddressesBeforeRelease(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			store := newFakeStore()
			svc := NewService(store, &fakeDeriver{})
			if existing {
				if _, err := svc.GetOrAssign(t.Context(), 101); err != nil {
					t.Fatal(err)
				}
			}
			incident := errors.New("deposit address differs from authenticated snapshot")
			validated := 0
			svc.SetSecurityGuards(func(context.Context) error { return nil }, func(_ context.Context, row Assignment) error {
				validated++
				if row.UserID != 101 || row.Family != FamilyEVM {
					t.Fatal("wrong assignment checked", row)
				}
				return incident
			})
			if got, err := svc.GetOrAssign(t.Context(), 101); got != nil || !errors.Is(err, incident) || validated != 1 {
				t.Fatal("unverified address escaped", got, validated, err)
			}
		})
	}
}
