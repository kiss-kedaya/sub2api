package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityRepository_ListGroupEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := repository.NewGroupQualityCheckRepository(db)
	now := time.Now().UTC()
	columns := []string{"id", "group_id", "account_id", "model_id", "status", "error_message", "created_at"}
	mock.ExpectQuery("SELECT r.id, ag.group_id, p.account_id").
		WithArgs(int64(43), 30).
		WillReturnRows(sqlmock.NewRows(columns).
			AddRow(11, 43, 7, "claude-sonnet-5", "degraded", "feet do not plausibly contact crank pedals", now).
			AddRow(10, 43, 7, "claude-sonnet-5", "success", "", now.Add(-time.Minute)))

	events, err := repo.ListGroupEvents(context.Background(), 43, 30)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, int64(11), events[0].ID)
	require.Equal(t, "degraded", events[0].Status)
	require.Equal(t, "claude-sonnet-5", events[0].ModelID)
	require.Contains(t, events[0].ErrorMessage, "crank pedals")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGroupQualityRepository_ListGroupEventsClampsLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := repository.NewGroupQualityCheckRepository(db)
	mock.ExpectQuery("SELECT r.id, ag.group_id, p.account_id").
		WithArgs(int64(9), 100).
		WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "account_id", "model_id", "status", "error_message", "created_at"}))

	events, err := repo.ListGroupEvents(context.Background(), 9, 5000)
	require.NoError(t, err)
	require.Empty(t, events)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGroupQualityRepository_GetGroupEventArtworkScoped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := repository.NewGroupQualityCheckRepository(db)

	mock.ExpectQuery("SELECT r.response_text").
		WithArgs(int64(43), int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"response_text"}).AddRow("<html></html>"))
	text, err := repo.GetGroupEventArtwork(context.Background(), 43, 11)
	require.NoError(t, err)
	require.Equal(t, "<html></html>", text)

	mock.ExpectQuery("SELECT r.response_text").
		WithArgs(int64(43), int64(999)).
		WillReturnRows(sqlmock.NewRows([]string{"response_text"}))
	_, err = repo.GetGroupEventArtwork(context.Background(), 43, 999)
	require.ErrorIs(t, err, service.ErrGroupQualityEventNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
