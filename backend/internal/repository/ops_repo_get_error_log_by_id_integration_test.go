//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGetErrorLogByID_APIKeyPrefixAndUpstreamStatus(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs RESTART IDENTITY CASCADE")
	repo := NewOpsRepository(integrationDB).(*opsRepository)

	var plainID int64
	err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO ops_error_logs (
			error_phase, error_type, severity, status_code, created_at
		) VALUES (
			'upstream', 'upstream_error', 'error', 500, NOW()
		) RETURNING id`,
	).Scan(&plainID)
	require.NoError(t, err)

	plain, err := repo.GetErrorLogByID(ctx, plainID)
	require.NoError(t, err)
	require.Empty(t, plain.APIKeyPrefix)

	validID, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
		ErrorPhase:   "request",
		ErrorType:    "api_error",
		Severity:     "error",
		StatusCode:   402,
		CreatedAt:    time.Now(),
		APIKeyPrefix: "sk-valid",
	})
	require.NoError(t, err)

	valid, err := repo.GetErrorLogByID(ctx, validID)
	require.NoError(t, err)
	require.Equal(t, "sk-valid", valid.APIKeyPrefix)

	zero := 0
	credentialFailureID, err := repo.InsertErrorLog(ctx, &service.OpsInsertErrorLogInput{
		ErrorPhase:         "account_auth",
		ErrorType:          "upstream_error",
		Severity:           "error",
		StatusCode:         503,
		UpstreamStatusCode: &zero,
		CreatedAt:          time.Now(),
	})
	require.NoError(t, err)

	credentialFailure, err := repo.GetErrorLogByID(ctx, credentialFailureID)
	require.NoError(t, err)
	require.NotNil(t, credentialFailure.UpstreamStatusCode)
	require.Zero(t, *credentialFailure.UpstreamStatusCode)
}

func TestListErrorLogsToleratesRowsWithoutUpstreamRequestID(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs RESTART IDENTITY CASCADE")
	repo := NewOpsRepository(integrationDB).(*opsRepository)

	// Regression: a row with no x-request-id / x-oneapi-request-id in any header
	// column used to produce SQL NULL, and scanning it into a string failed with
	// "converting NULL to string is unsupported", breaking the whole list API.
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO ops_error_logs (
			error_phase, error_type, severity, status_code, created_at,
			upstream_response_headers, upstream_request_headers
		) VALUES (
			'upstream', 'upstream_error', 'error', 502, NOW(),
			'{"content-type":"text/plain"}'::jsonb, '{"user-agent":"x"}'::jsonb
		)`)
	require.NoError(t, err)

	list, err := repo.ListErrorLogs(ctx, &service.OpsErrorLogFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, list.Errors, 1)
	require.Empty(t, list.Errors[0].UpstreamRequestID)
}

func TestListErrorLogsReadsUpstreamRequestIDFromHeaders(t *testing.T) {
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "TRUNCATE ops_error_logs RESTART IDENTITY CASCADE")
	repo := NewOpsRepository(integrationDB).(*opsRepository)

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO ops_error_logs (
			error_phase, error_type, severity, status_code, created_at,
			upstream_response_headers
		) VALUES (
			'upstream', 'upstream_error', 'error', 503, NOW(),
			'{"x-oneapi-request-id":"rid-from-header"}'::jsonb
		)`)
	require.NoError(t, err)

	list, err := repo.ListErrorLogs(ctx, &service.OpsErrorLogFilter{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, list.Errors, 1)
	require.Equal(t, "rid-from-header", list.Errors[0].UpstreamRequestID)
}