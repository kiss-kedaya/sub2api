package service

import "testing"

func TestAccountVisibleToOwner(t *testing.T) {
	own := int64(7)
	other := int64(8)
	if !AccountVisibleToOwner(nil, 7) {
		t.Fatal("legacy account should stay visible")
	}
	if !AccountVisibleToOwner(&own, 7) {
		t.Fatal("owner should see own upload")
	}
	if AccountVisibleToOwner(&other, 7) {
		t.Fatal("other admin upload must be hidden")
	}
	if !AccountVisibleToOwner(&other, 0) {
		t.Fatal("no scope must not hide accounts")
	}
}
