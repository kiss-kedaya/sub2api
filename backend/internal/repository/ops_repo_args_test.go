//go:build unit

package repository

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsInsertErrorLogArgsPreservesExplicitZeroUpstreamStatus(t *testing.T) {
	zero := 0
	args := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{UpstreamStatusCode: &zero})

	require.Len(t, args, 41)
	encoded, ok := args[27].(sql.NullInt64)
	require.True(t, ok)
	require.True(t, encoded.Valid)
	require.Zero(t, encoded.Int64)
}

func TestOpsNullableIntPointerDistinguishesNilZeroAndStatus(t *testing.T) {
	missing := opsNullableIntPointer(nil).(sql.NullInt64)
	require.False(t, missing.Valid)

	zeroValue := 0
	zero := opsNullableIntPointer(&zeroValue).(sql.NullInt64)
	require.True(t, zero.Valid)
	require.Zero(t, zero.Int64)

	statusValue := 503
	status := opsNullableIntPointer(&statusValue).(sql.NullInt64)
	require.True(t, status.Valid)
	require.EqualValues(t, 503, status.Int64)
}

func TestOpsSanitizeAndTruncateHelpers(t *testing.T) {
	t.Run("strip NUL and invalid UTF-8", func(t *testing.T) {
		require.Equal(t, "hello world", opsSanitizeText("hello\x00 world\xff\xfe"))
		require.Equal(t, sql.NullString{}, opsNullString(" \x00 \x00 "))
		require.Equal(t, sql.NullString{String: "abc", Valid: true}, opsNullString(" a\x00bc "))
	})

	t.Run("truncate by runes preserves UTF-8 boundaries", func(t *testing.T) {
		cjk := strings.Repeat("中", 105)
		got, ok := opsNullStringMax(cjk, 100).(sql.NullString)
		require.True(t, ok)
		require.True(t, got.Valid)
		require.Equal(t, 100, utf8.RuneCountInString(got.String))
		require.True(t, utf8.ValidString(got.String))

		// Exactly at limit stays unchanged.
		exact := strings.Repeat("a", 100)
		require.Equal(t, exact, opsTruncateRunes(exact, 100))
	})

	t.Run("sanitize JSONB strips unescaped u0000, replaces lone surrogates, and preserves valid pairs and escaped literals", func(t *testing.T) {
		cases := []struct {
			name string
			in   string
			want string
		}{
			{"plain u0000", `{"a":"\u0000"}`, `{"a":""}`},
			{"middle u0000 and raw NUL", `{"a":"x\u0000y` + "\x00" + `z"}`, `{"a":"xyz"}`},
			{"escaped backslash before u0000 kept", `{"a":"\\u0000"}`, `{"a":"\\u0000"}`},
			{"double backslash plus u0000 strips only u0000", `{"a":"\\\u0000"}`, `{"a":"\\"}`},
			{"escaped then unescaped", `{"a":"\\u0000\u0000"}`, `{"a":"\\u0000"}`},
			{"valid emoji surrogate pair kept", `{"a":"\uD83D\uDE00"}`, `{"a":"\uD83D\uDE00"}`},
			{"lone high surrogate replaced", `{"a":"\uD800x"}`, `{"a":"\ufffdx"}`},
			{"lone low surrogate replaced", `{"a":"\uDC00x"}`, `{"a":"\ufffdx"}`},
			{"high followed by valid pair", `{"a":"\uD83D\uD83D\uDE00"}`, `{"a":"\ufffd\uD83D\uDE00"}`},
			{"escaped lone surrogate kept", `{"a":"\\uD800"}`, `{"a":"\\uD800"}`},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := opsSanitizeJSONB(tc.in)
				require.Equal(t, tc.want, got)
				require.True(t, json.Valid([]byte(got)), "sanitized JSON must remain valid: %s", got)
			})
		}
	})

	t.Run("malformed JSONB falls back to NULL instead of aborting batch", func(t *testing.T) {
		bad := `{"truncated":`
		require.Equal(t, sql.NullString{}, opsNullJSONBString(&bad))
		valid := `[{"msg":"ok\u0000"}]`
		require.Equal(t, sql.NullString{String: `[{"msg":"ok"}]`, Valid: true}, opsNullJSONBString(&valid))
	})

	t.Run("empty or NUL-only severity falls back to NOT NULL default P2", func(t *testing.T) {
		argsEmpty := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{})
		require.Equal(t, "P2", argsEmpty[19])

		argsNul := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{Severity: " \x00 "})
		require.Equal(t, "P2", argsNul[19])
	})

	t.Run("header snapshot drops pure-NUL keys and cleans values", func(t *testing.T) {
		require.Nil(t, opsNullJSONBHeaderSnapshot(service.OpsHeaderSnapshot{"\x00": "v"}))
		encoded := opsNullJSONBHeaderSnapshot(service.OpsHeaderSnapshot{
			"x-req\x00-id": "abc\x00def",
		})
		require.Equal(t, `{"x-req-id":"abcdef"}`, encoded)
	})
}

func TestOpsInsertErrorLogArgsEnforcesAllVarcharLimits(t *testing.T) {
	args := opsInsertErrorLogArgs(&service.OpsInsertErrorLogInput{
		RequestID:        strings.Repeat("r", 100),
		ClientRequestID:  strings.Repeat("c", 100),
		Platform:         strings.Repeat("p", 50),
		Model:            strings.Repeat("m", 150),
		RequestPath:      strings.Repeat("/", 300),
		InboundEndpoint:  strings.Repeat("i", 300),
		UpstreamEndpoint: strings.Repeat("u", 300),
		RequestedModel:   strings.Repeat("q", 150),
		UpstreamModel:    strings.Repeat("w", 150),
		ErrorPhase:       strings.Repeat("e", 50),
		ErrorType:        strings.Repeat("t", 100),
		Severity:         strings.Repeat("s", 20),
		ErrorSource:      strings.Repeat("x", 100),
		ErrorOwner:       strings.Repeat("y", 50),
		APIKeyPrefix:     strings.Repeat("k", 50),
	})

	require.Len(t, args, 41)
	assertNullStrLen := func(idx, want int) {
		t.Helper()
		ns, ok := args[idx].(sql.NullString)
		require.True(t, ok)
		require.True(t, ns.Valid)
		require.Equal(t, want, utf8.RuneCountInString(ns.String))
	}
	assertStrLen := func(idx, want int) {
		t.Helper()
		s, ok := args[idx].(string)
		require.True(t, ok)
		require.Equal(t, want, utf8.RuneCountInString(s))
	}
	assertNullStrLen(0, 64)
	assertNullStrLen(1, 64)
	assertNullStrLen(7, 32)
	assertNullStrLen(8, 100)
	assertNullStrLen(9, 256)
	assertNullStrLen(11, 256)
	assertNullStrLen(12, 256)
	assertNullStrLen(13, 100)
	assertNullStrLen(14, 100)
	assertStrLen(17, 32)
	assertStrLen(18, 64)
	assertStrLen(19, 8)
	assertNullStrLen(25, 64)
	assertNullStrLen(26, 32)
	assertNullStrLen(37, 32)
}
