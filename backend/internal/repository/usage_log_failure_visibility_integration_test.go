//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageFailureHydrationScopesIdentityTimeAndRecoveredAttempts(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newUsageLogRepositoryWithSQL(tx.Client(), tx)
	ended := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	insert := func(request, client string, user, key, account int64, status int, at time.Time) {
		_, err := tx.ExecContext(ctx, `INSERT INTO ops_error_logs
            (request_id,client_request_id,user_id,api_key_id,account_id,status_code,error_phase,error_type,created_at,duration_ms)
            VALUES ($1,$2,$3,$4,$5,$6,'upstream','api_error',$7,10000)`, request, client, user, key, account, status, at)
		require.NoError(t, err)
	}
	row := func(id int64, request string) service.UsageLog {
		duration := 10000
		return service.UsageLog{ID: id, UserID: 10, APIKeyID: 20, AccountID: 30, RequestID: request, CreatedAt: ended, DurationMs: &duration, InputTokens: 10, ActualCost: .125}
	}
	// One real match, and collisions that must not expose another request's failure.
	insert("ops-terminal", "matched", 10, 20, 30, 502, ended)
	insert("ops-other-user", "other-user", 11, 20, 30, 500, ended)
	insert("ops-other-key", "other-key", 10, 21, 30, 500, ended)
	insert("ops-other-account", "other-account", 10, 20, 31, 500, ended)
	insert("ops-yesterday", "yesterday", 10, 20, 30, 500, ended.Add(-24*time.Hour))
	insert("ops-recovered", "recovered", 10, 20, 30, 200, ended)
	insert("direct-request", "", 10, 20, 30, 500, ended)
	insert("local-request", "", 10, 20, 30, 503, ended)
	// A successful retry with a reused caller ID must not inherit the earlier
	// attempt's failure. Completion time and elapsed duration both anchor the match.
	insert("ops-prior-attempt", "retried", 10, 20, 30, 500, ended.Add(-time.Minute))
	insert("ops-other-duration", "other-duration", 10, 20, 30, 500, ended)
	insert("ops-missing-duration", "missing-duration", 10, 20, 30, 502, ended)
	_, err := tx.ExecContext(ctx, "UPDATE ops_error_logs SET duration_ms=NULL WHERE request_id='ops-missing-duration'")
	require.NoError(t, err)
	differentDuration := 30000
	otherDuration := row(10, "client:other-duration")
	otherDuration.DurationMs = &differentDuration
	logs := []service.UsageLog{row(1, "client:matched"), row(2, "client:other-user"), row(3, "client:other-key"), row(4, "client:other-account"), row(5, "client:yesterday"), row(6, "client:recovered"), row(7, "direct-request"), row(8, "local:local-request"), row(9, "client:retried"), otherDuration, row(11, "client:missing-duration")}
	require.NoError(t, repo.HydrateUsageLogFailures(ctx, logs))
	require.Equal(t, 502, *logs[0].FailureStatusCode)
	for i := 1; i < 6; i++ {
		require.Nil(t, logs[i].FailureStatusCode, "collision/recovered row %d", i)
	}
	require.Equal(t, 500, *logs[6].FailureStatusCode)
	require.Equal(t, 503, *logs[7].FailureStatusCode)
	require.Nil(t, logs[8].FailureStatusCode, "successful retry must not borrow an earlier failure")
	require.Nil(t, logs[9].FailureStatusCode, "different elapsed time is not the same attempt")
	require.Equal(t, 502, *logs[10].FailureStatusCode, "historical Ops may lack timing but share a precise completion anchor")
	for _, log := range logs {
		require.Equal(t, .125, log.ActualCost)
		require.Equal(t, 10, log.InputTokens)
	}
}

func TestUsageFailureHydrationSkipsAmbiguousRapidRetryWithReusedClientID(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "failure-visibility@example.test"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "synthetic-visibility-key", Name: "visibility"})
	account := mustCreateAccount(t, client, &service.Account{Name: "visibility-account"})
	ended := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	duration := 200
	first := service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: "client:rapid-retry", CreatedAt: ended, DurationMs: &duration, InputTokens: 10, ActualCost: .125}
	second := first
	second.RequestID = "local:rapid-retry"
	second.CreatedAt = ended.Add(500 * time.Millisecond)
	_, err := repo.Create(ctx, &first)
	require.NoError(t, err)
	_, err = repo.Create(ctx, &second)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs
        (request_id,client_request_id,user_id,api_key_id,account_id,status_code,error_phase,error_type,created_at,duration_ms)
        VALUES ('synthetic-rapid-error','rapid-retry',$1,$2,$3,502,'upstream','api_error',$4,200)`, user.ID, key.ID, account.ID, ended)
	require.NoError(t, err)
	logs := []service.UsageLog{first, second}
	require.NoError(t, repo.HydrateUsageLogFailures(ctx, logs))
	require.Nil(t, logs[0].FailureStatusCode, "two nearby usage rows make caller-ID-only correlation ambiguous")
	require.Nil(t, logs[1].FailureStatusCode, "a successful rapid retry must not inherit an older failure")
}
