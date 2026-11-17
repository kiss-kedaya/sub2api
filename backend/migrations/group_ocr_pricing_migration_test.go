package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupOCRPriceMigrationIsNullableAndAdditive(t *testing.T) {
	content, err := FS.ReadFile("318_group_ocr_price_per_1k_pages.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS ocr_price_per_1k_pages DECIMAL(20,8)")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.ocr_price_per_1k_pages")
	require.Contains(t, sql, "per 1,000 pages")
	require.NotContains(t, sql, "NOT NULL")
	require.NotContains(t, sql, "UPDATE groups")
}
