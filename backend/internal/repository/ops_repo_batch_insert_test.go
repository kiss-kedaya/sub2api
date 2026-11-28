package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsRepositoryBatchInsertErrorLogsUsesOneMultiRowStatement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &opsRepository{db: db}
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL synchronous_commit = off")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ops_error_logs")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	inserted, err := repo.BatchInsertErrorLogs(context.Background(), []*service.OpsInsertErrorLogInput{
		{ErrorPhase: "upstream", ErrorType: "upstream_error", ErrorMessage: "one", CreatedAt: now},
		{ErrorPhase: "internal", ErrorType: "api_error", ErrorMessage: "two", CreatedAt: now},
		nil,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsRepositoryBatchInsertErrorLogsSetLocalFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &opsRepository{db: db}
	setLocalErr := errors.New("set local failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL synchronous_commit = off")).
		WillReturnError(setLocalErr)
	mock.ExpectRollback()

	inserted, err := repo.BatchInsertErrorLogs(context.Background(), []*service.OpsInsertErrorLogInput{
		{ErrorPhase: "upstream", ErrorType: "upstream_error", ErrorMessage: "one", CreatedAt: time.Now().UTC()},
	})
	require.ErrorIs(t, err, setLocalErr)
	require.Zero(t, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsRepositoryBatchInsertErrorLogsSanitizesAndTruncatesArgs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &opsRepository{db: db}
	now := time.Now().UTC()
	rawUpstreamErr := `[{"msg":"bad\u0000` + "\x00" + `text","keep":"\\u0000"}]`
	rawClientIP := "127.0.0.1\x00"
	rawUpMsg := "upstream\x00msg\xff"
	rawUpDetail := "detail\x00body"
	longModel := strings.Repeat("模", 120) + "\x00"

	input := &service.OpsInsertErrorLogInput{
		RequestID:            strings.Repeat("r", 80) + "\x00",
		ClientRequestID:      strings.Repeat("c", 80) + "\x00",
		ClientIP:             &rawClientIP,
		Platform:             strings.Repeat("p", 50) + "\x00",
		Model:                longModel,
		RequestPath:          strings.Repeat("/", 300) + "\x00",
		InboundEndpoint:      strings.Repeat("i", 300) + "\x00",
		UpstreamEndpoint:     strings.Repeat("u", 300) + "\x00",
		RequestedModel:       strings.Repeat("m", 130) + "\x00",
		UpstreamModel:        strings.Repeat("u", 130) + "\x00",
		UserAgent:            "ua\x00agent\xfe",
		ErrorPhase:           strings.Repeat("a", 40) + "\x00",
		ErrorType:            strings.Repeat("t", 80) + "\x00",
		Severity:             "critical-extra\x00",
		ErrorMessage:         "err\x00msg\xff",
		ErrorBody:            "err\x00body",
		ErrorSource:          strings.Repeat("s", 80) + "\x00",
		ErrorOwner:           strings.Repeat("o", 40) + "\x00",
		UpstreamErrorMessage: &rawUpMsg,
		UpstreamErrorDetail:  &rawUpDetail,
		UpstreamErrorsJSON:   &rawUpstreamErr,
		CreatedAt:            now,
		APIKeyPrefix:         strings.Repeat("k", 50) + "\x00",
		RequestHeaders: service.OpsHeaderSnapshot{
			"x-test\x00": "val\x00ue\xff",
			"\x00":       "dropped",
		},
	}

	expectedArgs := opsInsertErrorLogArgs(input)
	driverArgs := make([]driver.Value, len(expectedArgs))
	for i, arg := range expectedArgs {
		if ns, ok := arg.(sql.NullString); ok {
			if ns.Valid {
				require.NotContains(t, ns.String, "\x00")
				require.True(t, utf8.ValidString(ns.String))
				driverArgs[i] = ns.String
			} else {
				driverArgs[i] = nil
			}
		} else if ni, ok := arg.(sql.NullInt64); ok {
			if ni.Valid {
				driverArgs[i] = ni.Int64
			} else {
				driverArgs[i] = nil
			}
		} else {
			if s, ok := arg.(string); ok {
				require.NotContains(t, s, "\x00")
				require.True(t, utf8.ValidString(s))
			}
			driverArgs[i] = arg
		}
	}

	assertNullStringArg := func(idx int) sql.NullString {
		t.Helper()
		ns, ok := expectedArgs[idx].(sql.NullString)
		require.True(t, ok)
		return ns
	}
	require.Equal(t, 100, utf8.RuneCountInString(assertNullStringArg(8).String))
	require.Equal(t, 100, utf8.RuneCountInString(assertNullStringArg(13).String))
	require.Equal(t, 100, utf8.RuneCountInString(assertNullStringArg(14).String))
	require.True(t, json.Valid([]byte(assertNullStringArg(30).String)))
	require.Equal(t, `[{"msg":"badtext","keep":"\\u0000"}]`, assertNullStringArg(30).String)
	reqHeadersJSON, ok := expectedArgs[38].(string)
	require.True(t, ok)
	require.Equal(t, `{"x-test":"value"}`, reqHeadersJSON)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL synchronous_commit = off")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ops_error_logs")).
		WithArgs(driverArgs...).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	inserted, err := repo.BatchInsertErrorLogs(context.Background(), []*service.OpsInsertErrorLogInput{input})
	require.NoError(t, err)
	require.EqualValues(t, 1, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsRepositoryBatchInsertErrorLogsMultiChunkCommitsPerChunk(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &opsRepository{db: db}
	now := time.Now().UTC()
	inputs := make([]*service.OpsInsertErrorLogInput, opsErrorLogInsertChunkSize+2)
	for i := range inputs {
		inputs[i] = &service.OpsInsertErrorLogInput{
			ErrorPhase:   "upstream",
			ErrorType:    "upstream_error",
			ErrorMessage: "msg",
			CreatedAt:    now,
		}
	}

	// Chunk 1 commits cleanly via execAsyncCommit.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL synchronous_commit = off")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ops_error_logs")).
		WillReturnResult(sqlmock.NewResult(0, opsErrorLogInsertChunkSize))
	mock.ExpectCommit()

	// Chunk 2 fails and rolls back only chunk 2; returned count matches committed chunk 1.
	chunk2Err := errors.New("chunk 2 failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SET LOCAL synchronous_commit = off")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO ops_error_logs")).
		WillReturnError(chunk2Err)
	mock.ExpectRollback()

	inserted, err := repo.BatchInsertErrorLogs(context.Background(), inputs)
	require.ErrorIs(t, err, chunk2Err)
	require.EqualValues(t, opsErrorLogInsertChunkSize, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpsRepositoryBatchInsertSystemLogsTruncatesAllVarcharColumnsAndValidatesExtra(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	repo := &opsRepository{db: db}
	now := time.Now().UTC()

	mock.ExpectBegin()
	prep := mock.ExpectPrepare(regexp.QuoteMeta(`COPY "ops_system_logs"`))
	prep.ExpectExec().
		WithArgs(
			now,
			strings.Repeat("h", opsSystemLogMaxHostLen),
			strings.Repeat("e", opsSystemLogMaxLevelLen),
			strings.Repeat("c", opsSystemLogMaxComponentLen),
			"hello world",
			strings.Repeat("r", opsSystemLogMaxRequestIDLen),
			strings.Repeat("i", opsSystemLogMaxRequestIDLen),
			nil,
			nil,
			nil,
			strings.Repeat("p", opsSystemLogMaxPlatformLen),
			strings.Repeat("m", opsSystemLogMaxModelLen),
			"{}",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	prep.ExpectExec().WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	inserted, err := repo.BatchInsertSystemLogs(context.Background(), []*service.OpsInsertSystemLogInput{
		{
			CreatedAt:       now,
			Host:            strings.Repeat("h", 300) + "\x00",
			Level:           strings.Repeat("E", 30) + "\x00",
			Component:       strings.Repeat("c", 200) + "\x00",
			Message:         "hello\x00 world\xff",
			RequestID:       strings.Repeat("r", 200) + "\x00",
			ClientRequestID: strings.Repeat("i", 200) + "\x00",
			Platform:        strings.Repeat("p", 50) + "\x00",
			Model:           strings.Repeat("m", 200) + "\x00",
			ExtraJSON:       `{"broken":`,
		},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, inserted)
	require.NoError(t, mock.ExpectationsWereMet())
}
