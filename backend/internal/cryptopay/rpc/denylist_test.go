package rpc

import "testing"

func TestIsDeniedRPCEndpoint(t *testing.T) {
	denied := []string{
		"https://0.48.club",
		"https://0.48.club/",
		"https://rpc-bsc.48.club",
		"https://sub.rpc.48.club",
		"https://48.club",
		"0.48.club:8545",
		"https://0.48.CLUB",
	}
	for _, endpoint := range denied {
		if !IsDeniedRPCEndpoint(endpoint) {
			t.Fatalf("expected %q to be denied", endpoint)
		}
	}

	allowed := []string{
		"https://bsc-dataseed.binance.org",
		"https://bsc-rpc.publicnode.com",
		"https://rpc.ankr.com/bsc",
		"https://not-48.club.example.com",
		"https://cluster48.club.example",
		"",
	}
	for _, endpoint := range allowed {
		if IsDeniedRPCEndpoint(endpoint) {
			t.Fatalf("expected %q to be allowed", endpoint)
		}
	}
}

func TestSetEndpointsDropsDeniedHosts(t *testing.T) {
	p := NewPool("https://bsc-dataseed.binance.org, https://0.48.club ,https://bsc-rpc.publicnode.com")
	got := p.Endpoints()
	want := []string{"https://bsc-dataseed.binance.org", "https://bsc-rpc.publicnode.com"}
	if len(got) != len(want) {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("endpoints = %v, want %v", got, want)
		}
	}
}

func TestSetEndpointsKeepsOnlyDeniedHostEmpty(t *testing.T) {
	p := NewPool("https://0.48.club")
	if got := p.Endpoints(); len(got) != 0 {
		t.Fatalf("endpoints = %v, want none", got)
	}
}
