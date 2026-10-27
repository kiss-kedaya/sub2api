//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func (r *cachedBillingRepo) CommitChargedMutation(_ context.Context, id int64, token string, target time.Time, intent *service.MediaBillingIntent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failCommit {
		return errors.New("db unavailable")
	}
	record := r.liveLocked(id)
	mutation, pending := r.mutations[id]
	if record == nil || !pending || mutation.token != token || record.ChargeIntent != nil {
		return service.ErrGeminiCachedContentMutationConflict
	}
	record.BillingPending, record.ChargeIntent, record.ChargeTargetExpire = true, intent, &target
	delete(r.mutations, id)
	return nil
}

func TestGeminiCachedContentsCreateRecoversFrozenChargeAfterRestart(t *testing.T) {
	env := newCachedBillingEnv(t, 500, 900)
	env.upstream.createBody = cachedBillingCreateBody(time.Now().Add(100 * time.Hour))
	env.ledger.err = errors.New("ledger temporarily unavailable")
	rec := env.create()
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Len(t, env.repo.records, 1)
	var record *service.GeminiCachedContent
	for _, value := range env.repo.records {
		record = value
	}
	require.True(t, record.BillingPending)
	require.NotNil(t, record.ChargeIntent)
	require.Empty(t, env.usage.totals())
	_, err := env.h.geminiCachedContentService.Resolve(context.Background(), env.apiKey.ID, env.apiKey.GroupID, record.PublicName())
	require.ErrorIs(t, err, service.ErrGeminiCachedContentNotFound, "unpaid resource cannot be used")
	frozen := record.ChargeIntent.Usage.ActualCost
	env.group.RateMultiplier = 100
	env.ledger.err = nil
	restarted := service.NewGeminiCachedContentService(env.repo, nil, nil)
	restarted.SetBillingGateway(env.h.gatewayService)
	count, err := restarted.RecoverPendingCharges(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, env.ledger.applied)
	require.InDelta(t, frozen, env.usage.first().ActualCost, 1e-8, "recovery cannot reprice the operation")
	_, err = restarted.Resolve(context.Background(), env.apiKey.ID, env.apiKey.GroupID, record.PublicName())
	require.NoError(t, err)
	count, err = restarted.RecoverPendingCharges(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
	require.Equal(t, 1, env.ledger.applied)
}

func TestGeminiCachedContentsPatchRecoversAfterLedgerBeforeUsageCrash(t *testing.T) {
	env := newCachedBillingEnv(t, 1000, 900)
	baseline := time.Now().Add(time.Hour)
	env.seed(baseline)
	env.usage.failWrites = true
	rec := env.patch()
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	record := env.repo.records["abc"]
	require.True(t, record.BillingPending)
	require.True(t, record.ExpireTime.Equal(baseline), "unconfirmed extension never becomes visible")
	require.NotNil(t, record.ChargeTargetExpire)
	require.Equal(t, 1, env.ledger.applied)
	require.False(t, env.repo.pendingMutation(100), "persisted charge is no longer a rollback-only mutation")
	requestID := record.ChargeIntent.Command.RequestID
	env.usage.failWrites = false
	count, err := env.h.geminiCachedContentService.RecoverPendingCharges(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 1, env.ledger.applied, "same frozen command must deduplicate after ledger commit")
	require.Equal(t, requestID, env.usage.first().RequestID)
	require.WithinDuration(t, time.Now().Add(24*time.Hour), record.ExpireTime, 5*time.Second)
	require.False(t, record.BillingPending)
}

func (r *cachedBillingRepo) CompleteCharge(_ context.Context, id int64, request string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record := r.liveLocked(id)
	if record == nil {
		return service.ErrGeminiCachedContentNotFound
	}
	if record.ChargeIntent == nil {
		return nil
	}
	if record.ChargeIntent.Command.RequestID != request {
		return service.ErrGeminiCachedContentMutationConflict
	}
	if record.ChargeTargetExpire != nil {
		record.ExpireTime = *record.ChargeTargetExpire
	}
	record.BillingPending, record.ChargeIntent, record.ChargeTargetExpire = false, nil, nil
	return nil
}

func (r *cachedBillingRepo) ListPendingCharges(_ context.Context, limit int) ([]*service.GeminiCachedContent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*service.GeminiCachedContent
	for _, record := range r.records {
		if record.ChargeIntent != nil && r.liveLocked(record.ID) != nil && len(out) < limit {
			cp := *record
			out = append(out, &cp)
		}
	}
	return out, nil
}

type cachedContentLedger struct {
	service.UsageBillingRepository
	mu      sync.Mutex
	seen    map[string]string
	err     error
	applied int
}

func (r *cachedContentLedger) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	if r.seen == nil {
		r.seen = map[string]string{}
	}
	if previous, ok := r.seen[cmd.RequestID]; ok {
		if previous != cmd.RequestFingerprint {
			return nil, service.ErrUsageBillingRequestConflict
		}
		return &service.UsageBillingApplyResult{}, nil
	}
	r.seen[cmd.RequestID] = cmd.RequestFingerprint
	r.applied++
	return &service.UsageBillingApplyResult{Applied: true}, nil
}
