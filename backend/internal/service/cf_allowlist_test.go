package service

import "testing"

func TestNormalizePublicIP(t *testing.T) {
	if _, err := NormalizePublicIP("127.0.0.1"); err == nil {
		t.Fatal("loopback should fail")
	}
	if _, err := NormalizePublicIP("10.0.0.1"); err == nil {
		t.Fatal("private should fail")
	}
	got, err := NormalizePublicIP(" 8.8.8.8 ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8.8.8.8" {
		t.Fatalf("got %q", got)
	}
}

func TestCFAllowlistSlots(t *testing.T) {
	if cfAllowlistSlots(99) != 0 {
		t.Fatal("under threshold")
	}
	if cfAllowlistSlots(100) != 1 {
		t.Fatal("100 should be 1")
	}
	if cfAllowlistSlots(250) != 2 {
		t.Fatal("250 should be 2")
	}
	if cfAllowlistSlots(9999) != CFAllowlistMaxIPs {
		t.Fatal("cap")
	}
}
