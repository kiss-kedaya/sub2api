package config

import "testing"

func TestNormalizeRunMode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"SIMPLE", "simple"},
		{"standard", "standard"},
		{"invalid", "standard"},
		{"", "standard"},
	}

	for _, tt := range tests {
		result := NormalizeRunMode(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizeRunMode(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
