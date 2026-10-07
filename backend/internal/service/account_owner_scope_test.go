package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountVisibleToOwner(t *testing.T) {
	own := int64(7)
	other := int64(8)
	if AccountVisibleToOwner(nil, 7, false) {
		t.Fatal("legacy account must be hidden from an ordinary admin")
	}
	if !AccountVisibleToOwner(nil, 7, true) {
		t.Fatal("legacy account should be visible to the configured pool owner")
	}
	if !AccountVisibleToOwner(&own, 7, false) {
		t.Fatal("owner should see own upload")
	}
	if !AccountVisibleToOwner(&other, 7, true) {
		t.Fatal("the configured pool owner should see other admin uploads")
	}
	if AccountVisibleToOwner(&other, 7, false) {
		t.Fatal("another admin upload must stay hidden from ordinary admins")
	}
	if !AccountVisibleToOwner(&other, 0, false) {
		t.Fatal("no scope must not hide accounts")
	}
}

func TestAccountOwnerScopeUsesConfiguredUserID(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		adminID, configuredID int64
		wantAll               bool
	}{
		{"configured owner", 7, 7, true},
		{"different administrator", 8, 7, false},
		{"unconfigured", 7, 0, false},
		{"invalid configured owner", 7, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithAccountOwnerScope(context.Background(), tc.adminID, tc.configuredID)
			id, fullPool, ok := AccountOwnerScopeDetail(ctx)
			require.True(t, ok)
			require.Equal(t, tc.adminID, id)
			require.Equal(t, tc.wantAll, fullPool)
			zero, owner, other := int64(0), int64(7), int64(8)
			for _, createdBy := range []*int64{nil, &zero, &owner, &other} {
				want := tc.wantAll || (createdBy != nil && *createdBy == tc.adminID)
				require.Equal(t, want, AccountVisibleToOwner(createdBy, id, fullPool))
			}
		})
	}
}

func TestEnsureAccountOwnerScopePreservesOnlyMatchingIdentity(t *testing.T) {
	owner := WithAccountOwnerScope(context.Background(), 7, 7)
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		adminID int64
		wantAll bool
	}{
		{"account route preserves owner", owner, 7, true},
		{"scheduled route preserves owner", owner, 7, true},
		{"another identity cannot inherit owner", owner, 8, false},
		{"missing scope stays restricted", context.Background(), 7, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := EnsureAccountOwnerScope(tc.ctx, tc.adminID)
			id, all, ok := AccountOwnerScopeDetail(ctx)
			require.True(t, ok)
			require.Equal(t, tc.adminID, id)
			require.Equal(t, tc.wantAll, all)
		})
	}
}
