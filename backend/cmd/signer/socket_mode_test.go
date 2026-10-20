package main

import (
	"os"
	"testing"
)

func TestParseSocketModeAllowsOnlyOwnerOrIPCGroup(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want os.FileMode
	}{
		{"0600", 0o600},
		{"0660", 0o660},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			mode, err := parseSocketMode(tc.raw)
			if err != nil || mode != tc.want || mode&0o007 != 0 {
				t.Fatalf("parseSocketMode(%q) = %04o, %v; want %04o", tc.raw, mode, err, tc.want)
			}
		})
	}
}

func TestParseSocketModeRejectsBroadOrAmbiguousPermissions(t *testing.T) {
	for _, raw := range []string{
		"", "0777", "0666", "0644", "0664", "0700", "0000", "04660",
		"600", "660", "384", "432", "0o600", "0o660", "0x180",
		"00600", "+0600", "-0600", " 0600", "0660 ", "0600\n", "invalid",
	} {
		t.Run(raw, func(t *testing.T) {
			mode, err := parseSocketMode(raw)
			if err == nil || mode != 0 {
				t.Fatalf("unsafe socket mode %q accepted: %04o, %v", raw, mode, err)
			}
		})
	}
}
