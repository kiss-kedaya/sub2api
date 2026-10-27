package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelOperationsQueryFailureIsNotSuccessfulZero(t *testing.T) {
	db, err := sql.Open("postgres", "")
	require.NoError(t, err)
	require.NoError(t, db.Close())
	repo := &channelRepository{db: db}
	got := repo.operationsSection(context.Background(), `SELECT '{}'::jsonb`)
	require.Equal(t, "query_failed", got.Status)
	require.JSONEq(t, "null", string(got.Data))
	require.NotEmpty(t, got.Error)
	require.NotContains(t, got.Error, "database is closed")
}
