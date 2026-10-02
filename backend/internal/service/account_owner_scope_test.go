package service

import (
	"context"
	"testing"
)

func TestAccountVisibleToOwner(t *testing.T) {
	own := int64(7)
	other := int64(8)
	if AccountVisibleToOwner(nil, 7, false) {
		t.Fatal("legacy account must be hidden from an ordinary admin")
	}
	if !AccountVisibleToOwner(nil, 7, true) {
		t.Fatal("legacy account should stay visible to admin@sub2api.local")
	}
	if !AccountVisibleToOwner(&own, 7, false) {
		t.Fatal("owner should see own upload")
	}
	if AccountVisibleToOwner(&other, 7, true) {
		t.Fatal("another admin upload must stay hidden")
	}
	if !AccountVisibleToOwner(&other, 0, false) {
		t.Fatal("no scope must not hide accounts")
	}
}

func TestWithAccountOwnerScopeLegacyEmail(t *testing.T) {
	ctx := WithAccountOwnerScope(context.Background(), 3, " Admin@sub2api.local ")
	id, seeLegacy, ok := AccountOwnerScopeDetail(ctx)
	if !ok || id != 3 || !seeLegacy {
		t.Fatalf("primary admin scope = %d %v %v", id, seeLegacy, ok)
	}
	ctx = WithAccountOwnerScope(context.Background(), 4, "other@example.com")
	id, seeLegacy, ok = AccountOwnerScopeDetail(ctx)
	if !ok || id != 4 || seeLegacy {
		t.Fatalf("ordinary admin scope = %d %v %v", id, seeLegacy, ok)
	}
}
