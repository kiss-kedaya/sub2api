package repository

import (
	"strings"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
)

// TestCodexOverdraftRateLimitPredicate is intentionally a SQL-shape test. The
// predicate is applied before the repository's defensive Go filter, so a
// regression here can silently starve eligible accounts behind ordinary
// future 429 rows (the candidate query has a bounded LIMIT).
func TestCodexOverdraftRateLimitPredicate(t *testing.T) {
	selector := entsql.Select().From(entsql.Table("accounts"))
	codexOverdraftRateLimitPredicate()(selector)
	query, args := selector.Query()
	require.Empty(t, args, "quota evidence predicate should not interpolate runtime values")

	normalized := normalizeSQLWhitespace(strings.ToLower(query))
	// Preserve the normal rate-limit behavior for accounts without a live
	// reset, and require explicit Codex evidence for a future reset.
	require.Contains(t, normalized, "rate_limit_reset_at")
	require.Contains(t, normalized, "is null")
	require.Contains(t, normalized, "<= now()")
	require.Contains(t, normalized, "> now()")
	require.Contains(t, normalized, "codex_5h_used_percent")
	require.Contains(t, normalized, "codex_7d_used_percent")
	require.Contains(t, normalized, ">= 95")
	// Legacy JSON values must be validated before casts. Keep this assertion
	// broad enough for Ent/Postgres formatting changes while guarding the
	// important no-500 property for malformed snapshots.
	require.GreaterOrEqual(t, strings.Count(normalized, "pg_input_is_valid"), 6)
	require.GreaterOrEqual(t, strings.Count(normalized, "case when"), 6)
	require.Contains(t, normalized, "between 0 and 1000")
	require.Contains(t, normalized, "make_interval")
	// A plain rate-reset OR would re-admit every future 429. The future branch
	// must be coupled to the two quota windows instead.
	require.Contains(t, normalized, "rate_limit_reset_at")
	require.Contains(t, normalized, "codex_usage_updated_at")
	require.GreaterOrEqual(t, strings.Count(normalized, "isfinite"), 2)
}
