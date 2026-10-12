package addresses

import "testing"

// sortBy reaches raw SQL, so the whitelist and its fallback are the only
// thing standing between an admin query string and injection.
func TestOrderByClauseWhitelistsColumns(t *testing.T) {
	tests := []struct {
		sortBy, sortDir, want string
	}{
		{"credited_total", "desc", " ORDER BY COALESCE(d.credited, 0) DESC, a.id DESC"},
		{"deposit_count", "asc", " ORDER BY COALESCE(d.cnt, 0) ASC, a.id DESC"},
		// Injections and unknowns collapse to the stable default.
		{"id; DROP TABLE deposit_addresses", "asc", " ORDER BY a.id ASC, a.id DESC"},
		{"", "", " ORDER BY a.id DESC, a.id DESC"},
		{"1=1 UNION SELECT", "asc", " ORDER BY a.id ASC, a.id DESC"},
	}
	for _, tt := range tests {
		if got := orderByClause(tt.sortBy, tt.sortDir); got != tt.want {
			t.Errorf("orderByClause(%q, %q) = %q, want %q", tt.sortBy, tt.sortDir, got, tt.want)
		}
	}
}
