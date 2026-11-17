package apicompat

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestCacheCreationTokensFromUsage(t *testing.T) {
	cases := []struct {
		name    string
		usage   string
		tokens  int
		present bool
	}{
		{"missing", `{"input_tokens":10}`, 0, false},
		{"nested wins even when zero", `{"input_tokens_details":{"cache_write_tokens":0},"cache_creation_input_tokens":150}`, 0, true},
		{"nested positive", `{"prompt_tokens_details":{"cache_creation_tokens":70}}`, 70, true},
		{"flat explicit zero is present", `{"cache_creation_input_tokens":0}`, 0, true},
		{"flat zero alias does not hide the populated alias", `{"cache_write_tokens":0,"cache_creation_input_tokens":150}`, 150, true},
		{"flat first positive", `{"cache_write_tokens":40,"cache_creation_input_tokens":150}`, 40, true},
		{"negative is clamped", `{"input_tokens_details":{"cache_write_tokens":-5}}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, present := CacheCreationTokensFromUsage(gjson.Parse(tc.usage))
			if tokens != tc.tokens || present != tc.present {
				t.Fatalf("got (%d, %v), want (%d, %v)", tokens, present, tc.tokens, tc.present)
			}
		})
	}
}

func TestCapCacheCreationBreakdownKeepsPartialSplit(t *testing.T) {
	cases := []struct {
		name              string
		aggregate, m5, h1 int
		want5m, want1h    int
	}{
		{"partial 5m stays as reported", 200, 80, 0, 80, 0},
		{"partial 1h stays as reported", 200, 0, 120, 0, 120},
		{"complete split unchanged", 200, 80, 120, 80, 120},
		{"details above aggregate are capped proportionally", 200, 200, 200, 100, 100},
		{"no aggregate keeps legacy details", 0, 30, 40, 30, 40},
		{"negative details are clamped", 200, -5, 120, 0, 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m5, h1 := CapCacheCreationBreakdown(tc.aggregate, tc.m5, tc.h1)
			if m5 != tc.want5m || h1 != tc.want1h {
				t.Fatalf("got (%d, %d), want (%d, %d)", m5, h1, tc.want5m, tc.want1h)
			}
		})
	}
}

func TestNormalizeCacheCreationBreakdown(t *testing.T) {
	cases := []struct {
		name              string
		aggregate, m5, h1 int
		want5m, want1h    int
	}{
		{"no details stay absent", 200, 0, 0, 0, 0},
		{"complete split unchanged", 200, 80, 120, 80, 120},
		{"partial 5m: remainder is 5m", 200, 80, 0, 200, 0},
		{"partial 1h: remainder is 5m", 200, 0, 120, 80, 120},
		{"details above aggregate are capped proportionally", 200, 200, 200, 100, 100},
		{"only 1h above aggregate", 200, 0, 500, 0, 200},
		{"no aggregate keeps legacy details", 0, 30, 40, 30, 40},
		{"negative details are clamped", 200, -5, 120, 80, 120},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m5, h1 := NormalizeCacheCreationBreakdown(tc.aggregate, tc.m5, tc.h1)
			if m5 != tc.want5m || h1 != tc.want1h {
				t.Fatalf("got (%d, %d), want (%d, %d)", m5, h1, tc.want5m, tc.want1h)
			}
			if tc.aggregate > 0 && m5+h1 > tc.aggregate {
				t.Fatalf("split %d+%d exceeds aggregate %d", m5, h1, tc.aggregate)
			}
		})
	}
}
