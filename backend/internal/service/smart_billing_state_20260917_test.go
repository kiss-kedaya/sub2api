//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The fake wallet models cumulative holds, including authorized zero holds.
// Pricing, guard ownership, tracking, and settlement use the real services.
type smartRouteWallet struct {
	*preauthorizationWalletStub
	balance        float64
	reserved       float64
	state          LiveBalanceAttemptState
	userID         int64
	attemptID      string
	authorizations int
	topUpResult    *LiveBalanceResult
	applyThenFail  bool
}

func (w *smartRouteWallet) AuthorizeExistingLiveBalance(ctx context.Context, userID int64, attemptID string, hold float64) (LiveBalanceResult, error) {
	w.authorizations++
	if w.state != 0 {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: w.state}, nil
	}
	w.userID, w.attemptID = userID, attemptID
	w.balance -= hold
	w.reserved, w.state = hold, LiveBalanceAttemptAuthorized
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: w.state, ReservedAmount: hold}, nil
}

func (w *smartRouteWallet) TopUpLiveBalance(ctx context.Context, userID int64, attemptID string, target float64) (LiveBalanceResult, error) {
	w.topUpCalls++
	if err := ctx.Err(); err != nil {
		return LiveBalanceResult{}, err
	}
	if w.topUpResult != nil {
		return *w.topUpResult, nil
	}
	if w.topUpErr != nil && !w.applyThenFail {
		return LiveBalanceResult{}, w.topUpErr
	}
	if w.userID != userID || w.attemptID != attemptID || w.state != LiveBalanceAttemptAuthorized || target < w.reserved {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: w.state}, nil
	}
	delta := QuantizeUsageBillingAmount(target - w.reserved)
	if delta > w.balance {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeInsufficient, State: w.state, ReservedAmount: w.reserved}, nil
	}
	w.balance = QuantizeUsageBillingAmount(w.balance - delta)
	w.reserved = target
	if w.applyThenFail {
		return LiveBalanceResult{}, errors.New("reply lost after top-up")
	}
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: w.state, ReservedAmount: target}, nil
}

func (w *smartRouteWallet) RefundLiveBalance(ctx context.Context, userID int64, attemptID string) (LiveBalanceResult, error) {
	w.refundCalls++
	if userID != w.userID || attemptID != w.attemptID || w.state != LiveBalanceAttemptAuthorized {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: w.state}, nil
	}
	w.balance = QuantizeUsageBillingAmount(w.balance + w.reserved)
	w.state = LiveBalanceAttemptRefunded
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: w.state}, nil
}

func (w *smartRouteWallet) FinalizeLiveBalance(ctx context.Context, userID int64, attemptID string, actual float64) (LiveBalanceResult, error) {
	w.finalizeCalls++
	if userID != w.userID || attemptID != w.attemptID || w.state != LiveBalanceAttemptAuthorized {
		return LiveBalanceResult{Outcome: LiveBalanceOutcomeConflict, State: w.state}, nil
	}
	w.balance = QuantizeUsageBillingAmount(w.balance + w.reserved - actual)
	w.lastActual = actual
	w.state = LiveBalanceAttemptFinalized
	return LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: w.state, ActualAmount: actual}, nil
}

func smartRouteGuard(t *testing.T, rate float64) (*preauthorizationFixture, *smartRouteWallet, *BalancePreauthorizationGuard, BalancePreauthorizationRequest) {
	t.Helper()
	f := newPreauthorizationFixture()
	f.service.costCalculator = NewBillingService(&config.Config{}, nil)
	w := &smartRouteWallet{preauthorizationWalletStub: f.wallet, balance: 10}
	f.service.wallet, f.service.watermarkWallet = w, w
	req := balancePreauthorizationTestRequest()
	req.CostInput.Model, req.CostInput.ServiceTier, req.CostInput.RateMultiplier = "gpt-5.1", "", rate
	g, err := f.service.Preauthorize(context.Background(), req)
	require.NoError(t, err)
	req.RequestID, req.AuthorizationFingerprint = "", ""
	return f, w, g, req
}

func TestSmartBillingRepriceFreeToPaidSameAttempt(t *testing.T) {
	f, w, g, req := smartRouteGuard(t, 0)
	require.Zero(t, g.HoldAmount())
	require.Equal(t, LiveBalanceAttemptAuthorized, w.state)
	original := *f.repo.prepared
	req.CostInput.GroupID = i64p(202)
	req.CostInput.RateMultiplier = 2
	req.AuthorizationFingerprint = "route-specific-fingerprint-must-not-replace-durable-identity"
	require.Error(t, g.RepriceForRoute(context.Background(), req))
	req.AuthorizationFingerprint = ""
	estimate, err := f.service.estimateHold(context.Background(), req)
	require.NoError(t, err)
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.Equal(t, estimate.HoldAmount, g.HoldAmount())
	require.NotNil(t, g.core.outputHoldTracker)
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.Equal(t, 1, w.topUpCalls)
	require.NoError(t, g.ObserveStreamingOutput(context.Background(), 1024))
	require.Greater(t, g.HoldAmount(), estimate.HoldAmount)
	require.Equal(t, 2, w.topUpCalls)
	require.Equal(t, 1, w.authorizations)
	require.Equal(t, "request-1:7", w.attemptID)
	original.HoldAmount = g.HoldAmount()
	require.Equal(t, original, *f.repo.prepared)
	require.Equal(t, 0, w.refundCalls)
	require.NoError(t, g.Finalize(context.Background(), 0.03, "final-usage-fingerprint"))
	require.InDelta(t, 9.97, w.balance, 1e-8)
	require.Equal(t, "final-usage-fingerprint", f.repo.finalizedFingerprint)
}

func TestSmartBillingRepriceLowerPriceUsesHeldSurplus(t *testing.T) {
	_, w, g, req := smartRouteGuard(t, 100)
	held := g.HoldAmount()
	req.CostInput.RateMultiplier = 1
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.Equal(t, held, g.HoldAmount())
	require.NoError(t, g.ObserveStreamingOutput(context.Background(), 512))
	require.Zero(t, w.topUpCalls, "new output must use the already-held surplus")
	require.NoError(t, g.ObserveStreamingOutput(context.Background(), 100000))
	require.Equal(t, 1, w.topUpCalls)
	require.Greater(t, g.HoldAmount(), held)
	require.NoError(t, g.Refund(context.Background()))
	require.InDelta(t, 10, w.balance, 1e-8)
}

func TestSmartBillingRepricePerRequestEstimate(t *testing.T) {
	_, w, g, req := smartRouteGuard(t, 0)
	req.EstimateKind = PreauthorizationEstimatePerRequest
	req.CostInput.Resolver = &ModelPricingResolver{}
	req.CostInput.Resolved = &ResolvedPricing{Mode: BillingModePerRequest, DefaultPerRequestPrice: 0.25}
	req.CostInput.RateMultiplier = 2
	req.PerRequestEstimate.RequestCount = 3
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.InDelta(t, 1.5, g.HoldAmount(), 1e-8)
	require.Zero(t, g.ReservedOutputTokens())
	require.Nil(t, g.core.outputHoldTracker)
	req.PerRequestEstimate.UsageUnits = 4.5
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.InDelta(t, 2.25, g.HoldAmount(), 1e-8)
	req.PerRequestEstimate.UsageUnits = 1
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.InDelta(t, 2.25, g.HoldAmount(), 1e-8)
	require.Equal(t, 2, w.topUpCalls)
	require.NoError(t, g.Finalize(context.Background(), 0.5, "per-request-usage"))
	require.InDelta(t, 9.5, w.balance, 1e-8)
}

func TestSmartBillingRepriceAmbiguousTopUpRetryIsIdempotent(t *testing.T) {
	_, w, g, req := smartRouteGuard(t, 1)
	req.CostInput.RateMultiplier = 3
	w.applyThenFail = true
	require.Error(t, g.RepriceForRoute(context.Background(), req))
	balance := w.balance
	w.applyThenFail = false
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.Equal(t, balance, w.balance)
	require.Equal(t, w.reserved, g.HoldAmount())
	require.NoError(t, g.Refund(context.Background()))
	require.InDelta(t, 10, w.balance, 1e-8)
}

func TestSmartBillingRepriceSubscriptionRoundTrip(t *testing.T) {
	_, w, g, req := smartRouteGuard(t, 1)
	held := g.HoldAmount()
	req.BillingType = BillingTypeSubscription
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.Nil(t, g.core.outputHoldTracker)
	require.Zero(t, g.ReservedOutputTokens())
	require.Equal(t, held, g.HoldAmount())
	require.True(t, g.IsCurrentOwner())
	req.BillingType, req.CostInput.RateMultiplier = BillingTypeBalance, 3
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.NotNil(t, g.core.outputHoldTracker)
	require.Greater(t, g.HoldAmount(), held)
	require.Equal(t, 1, w.authorizations)
	require.Zero(t, w.refundCalls)
	req.BillingType = BillingTypeSubscription
	require.NoError(t, g.RepriceForRoute(context.Background(), req))
	require.NoError(t, g.Finalize(context.Background(), 0, "subscription-usage"))
	require.InDelta(t, 10, w.balance, 1e-8)
	require.Equal(t, 1, w.refundCalls)
}

func TestSmartBillingRepriceRejectsObservedOutput(t *testing.T) {
	for _, rate := range []float64{0, 1} {
		t.Run(map[bool]string{true: "free", false: "paid"}[rate == 0], func(t *testing.T) {
			_, w, g, req := smartRouteGuard(t, rate)
			require.NoError(t, g.ObserveStreamingOutput(context.Background(), 1))
			calls := w.topUpCalls
			for _, mode := range []int8{BillingTypeBalance, BillingTypeSubscription} {
				req.BillingType = mode
				require.ErrorIs(t, g.RepriceForRoute(context.Background(), req), ErrBalancePreauthorizationOutputObserved)
			}
			require.Equal(t, calls, w.topUpCalls)
		})
	}
}

func TestSmartBillingRepriceFailsClosedAndRefunds(t *testing.T) {
	for _, name := range []string{"insufficient", "unavailable", "ambiguous", "not-found", "under-reserved", "canceled", "pricing-error", "wrong-user", "wrong-key", "wrong-request"} {
		t.Run(name, func(t *testing.T) {
			_, w, g, req := smartRouteGuard(t, 1)
			held, tracker := g.HoldAmount(), g.core.outputHoldTracker
			req.CostInput.RateMultiplier = 5
			ctx := context.Background()
			switch name {
			case "insufficient":
				w.balance = 0
			case "unavailable":
				w.topUpErr = errors.New("wallet unavailable")
			case "ambiguous":
				w.applyThenFail = true
			case "not-found":
				w.topUpResult = &LiveBalanceResult{Outcome: LiveBalanceOutcomeNotFound}
			case "under-reserved":
				w.topUpResult = &LiveBalanceResult{Outcome: LiveBalanceOutcomeApplied, State: LiveBalanceAttemptAuthorized, ReservedAmount: held}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "pricing-error":
				req.CostInput.Model = "unpriced-smart-billing-test-model"
			case "wrong-user":
				req.UserID++
			case "wrong-key":
				req.APIKeyID++
			case "wrong-request":
				req.RequestID = "another-request"
			}
			err := g.RepriceForRoute(ctx, req)
			require.Error(t, err)
			if name == "insufficient" {
				require.ErrorIs(t, err, ErrBalanceWithholdingFailed)
			}
			require.Same(t, tracker, g.core.outputHoldTracker)
			require.Equal(t, held, g.HoldAmount())
			require.True(t, g.IsCurrentOwner())
			require.NoError(t, g.Refund(context.Background()))
			if name != "insufficient" {
				require.InDelta(t, 10, w.balance, 1e-8)
			}
			require.Equal(t, 1, w.authorizations)
		})
	}
}

func TestSmartBillingRepriceOwnershipAndTerminalStates(t *testing.T) {
	_, _, g, req := smartRouteGuard(t, 1)
	worker, ok := g.TransferToWorker()
	require.True(t, ok)
	require.ErrorIs(t, g.RepriceForRoute(context.Background(), req), ErrBalancePreauthorizationOwnershipTransferred)
	require.NoError(t, g.Refund(context.Background()))
	require.NoError(t, worker.RepriceForRoute(context.Background(), req))
	require.NoError(t, worker.Refund(context.Background()))
	require.ErrorIs(t, worker.RepriceForRoute(context.Background(), req), ErrBalancePreauthorizationAlreadyRefunded)
	_, _, settled, req := smartRouteGuard(t, 1)
	require.NoError(t, settled.Finalize(context.Background(), 0.01, "settled"))
	require.ErrorIs(t, settled.RepriceForRoute(context.Background(), req), ErrBalancePreauthorizationAlreadyFinalized)
}

func TestSmartBillingRepriceConcurrentObservationAndReaders(t *testing.T) {
	_, w, g, req := smartRouteGuard(t, 1)
	req.CostInput.RateMultiplier = 2
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := g.RepriceForRoute(context.Background(), req)
			if err != nil && !errors.Is(err, ErrBalancePreauthorizationOutputObserved) {
				t.Error(err)
			}
			if err := g.ObserveStreamingOutput(context.Background(), 32); err != nil {
				t.Error(err)
			}
			g.HoldAmount()
			g.ReservedOutputTokens()
		}()
	}
	wg.Wait()
	require.NoError(t, g.Refund(context.Background()))
	require.InDelta(t, 10, w.balance, 1e-8)
}

type smartRouteSubscriptionRepo struct {
	UserSubscriptionRepository
	sub             *UserSubscription
	err             error
	calls           int
	userID, groupID int64
}

func (r *smartRouteSubscriptionRepo) GetActiveByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	r.calls++
	r.userID, r.groupID = userID, groupID
	return r.sub, r.err
}

func smartRouteSubscriptionKey() (*APIKey, *UserSubscription) {
	group := &Group{ID: 202, SubscriptionType: SubscriptionTypeSubscription, Status: StatusActive, RateMultiplier: 2}
	key := &APIKey{ID: 7, UserID: 42, User: &User{ID: 42}, GroupID: &group.ID, Group: group}
	sub := &UserSubscription{ID: 302, UserID: 42, GroupID: 202, Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}
	return key, sub
}

func TestSmartBillingResolveRouteSubscription(t *testing.T) {
	for _, engine := range []string{"gateway", "openai"} {
		for _, name := range []string{"standard", "reuse", "switch", "missing-repo", "missing", "repo-error", "nil", "wrong-user", "wrong-group", "inactive", "expired", "zero-expiry", "future", "deleted", "bad-current"} {
			t.Run(engine+"/"+name, func(t *testing.T) {
				key, sub := smartRouteSubscriptionKey()
				repo := &smartRouteSubscriptionRepo{sub: sub}
				current := &UserSubscription{ID: 301, UserID: 42, GroupID: 101, Status: SubscriptionStatusActive}
				var repository UserSubscriptionRepository = repo
				switch name {
				case "standard":
					key.Group.SubscriptionType = SubscriptionTypeStandard
				case "reuse":
					current = sub
					repository = nil
				case "missing-repo":
					repository = nil
				case "missing":
					repo.err = ErrSubscriptionNotFound
				case "repo-error":
					repo.err = errors.New("repo unavailable")
				case "nil":
					repo.sub = nil
				case "wrong-user":
					sub.UserID++
				case "wrong-group":
					sub.GroupID++
				case "inactive":
					sub.Status = StatusDisabled
				case "expired":
					sub.ExpiresAt = time.Now().Add(-time.Second)
				case "zero-expiry":
					sub.ExpiresAt = time.Time{}
				case "future":
					sub.StartsAt = time.Now().Add(time.Minute)
				case "deleted":
					now := time.Now()
					sub.DeletedAt = &now
				case "bad-current":
					current = sub
					sub.Status = StatusDisabled
				}
				resolve := (&GatewayService{userSubRepo: repository}).ResolveAPIKeyRouteSubscription
				if engine == "openai" {
					resolve = (&OpenAIGatewayService{userSubRepo: repository}).ResolveAPIKeyRouteSubscription
				}
				got, err := resolve(context.Background(), key, current)
				switch name {
				case "standard":
					require.NoError(t, err)
					require.Nil(t, got)
					require.Zero(t, repo.calls)
				case "reuse":
					require.NoError(t, err)
					require.Same(t, sub, got)
					require.Zero(t, repo.calls)
				case "switch":
					require.NoError(t, err)
					require.Same(t, sub, got)
					require.Equal(t, int64(42), repo.userID)
					require.Equal(t, int64(202), repo.groupID)
				default:
					require.Error(t, err)
					require.Nil(t, got)
				}
			})
		}
	}
}

func TestSmartBillingUsageRejectsMismatchedSubscription(t *testing.T) {
	for _, engine := range []string{"gateway", "openai"} {
		for _, mismatch := range []string{"group", "user", "standard", "key-group", "usage-user"} {
			t.Run(engine+"/"+mismatch, func(t *testing.T) {
				key, sub := smartRouteSubscriptionKey()
				user := key.User
				switch mismatch {
				case "group":
					sub.GroupID = 101
				case "user":
					sub.UserID++
				case "standard":
					key.Group.SubscriptionType = SubscriptionTypeStandard
				case "key-group":
					key.GroupID = i64p(101)
				case "usage-user":
					user = &User{ID: 43}
				}
				logs := &openAIRecordUsageLogRepoStub{inserted: true}
				repo := &openAIRecordUsageBillingRepoStub{}
				account := &Account{ID: 9, Platform: PlatformOpenAI}
				var err error
				if engine == "openai" {
					svc := newOpenAIRecordUsageServiceForTest(logs, nil, nil, nil)
					svc.usageBillingRepo = repo
					err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{APIKey: key, User: user, Account: account, Subscription: sub,
						Result: &OpenAIForwardResult{RequestID: "wrong-sub", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 1000}}})
				} else {
					svc := newGatewayRecordUsageServiceForTest(logs, nil, nil)
					svc.usageBillingRepo = repo
					err = svc.RecordUsage(context.Background(), &RecordUsageInput{APIKey: key, User: user, Account: account, Subscription: sub,
						Result: &ForwardResult{RequestID: "wrong-sub", Model: "gpt-5.1", Usage: ClaudeUsage{InputTokens: 1000}}})
				}
				require.ErrorIs(t, err, ErrSubscriptionInvalid)
				require.Nil(t, repo.lastCmd)
				require.Nil(t, logs.lastLog)
				if mismatch != "usage-user" {
					require.Equal(t, BillingTypeBalance, BalancePreauthorizationBillingType(key, sub))
				}
			})
		}
	}
}

func TestSmartBillingUsageSubscriptionRefundsRetainedHold(t *testing.T) {
	for _, engine := range []string{"gateway", "openai"} {
		t.Run(engine, func(t *testing.T) {
			_, w, g, req := smartRouteGuard(t, 1)
			req.BillingType = BillingTypeSubscription
			require.NoError(t, g.RepriceForRoute(context.Background(), req))
			key, sub := smartRouteSubscriptionKey()
			logs := &openAIRecordUsageLogRepoStub{inserted: true}
			repo := &openAIRecordUsageBillingRepoStub{}
			ctx := ContextWithBalancePreauthorizationGuard(context.Background(), g)
			account := &Account{ID: 9, Platform: PlatformOpenAI}
			if engine == "openai" {
				svc := newOpenAIRecordUsageServiceForTest(logs, nil, nil, nil)
				svc.usageBillingRepo = repo
				require.NoError(t, svc.RecordUsage(ctx, &OpenAIRecordUsageInput{APIKey: key, User: key.User, Account: account, Subscription: sub,
					Result: &OpenAIForwardResult{RequestID: "request-1", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 1000}}}))
			} else {
				svc := newGatewayRecordUsageServiceForTest(logs, nil, nil)
				svc.usageBillingRepo = repo
				require.NoError(t, svc.RecordUsage(ctx, &RecordUsageInput{APIKey: key, User: key.User, Account: account, Subscription: sub,
					Result: &ForwardResult{RequestID: "request-1", Model: "gpt-5.1", Usage: ClaudeUsage{InputTokens: 1000}}}))
			}
			require.Equal(t, sub.ID, *repo.lastCmd.SubscriptionID)
			require.Greater(t, repo.lastCmd.SubscriptionCost, 0.0)
			require.Zero(t, repo.lastCmd.BalanceCost)
			require.True(t, repo.lastCmd.BalancePreauthorized)
			require.Equal(t, BillingTypeSubscription, BalancePreauthorizationBillingType(key, sub))
			require.Equal(t, 1, w.refundCalls)
			require.InDelta(t, 10, w.balance, 1e-8)
		})
	}
}

func TestSmartBillingSubscriptionIdentityLegacyFields(t *testing.T) {
	key, sub := smartRouteSubscriptionKey()
	key.GroupID = nil
	require.NoError(t, validateAPIKeyRouteSubscriptionIdentity(key, key.User, sub))
	require.Equal(t, BillingTypeSubscription, BalancePreauthorizationBillingType(key, sub))
	sub.UserID, sub.GroupID = 0, 0
	require.NoError(t, validateAPIKeyRouteSubscriptionIdentity(key, key.User, sub))
	key.UserID, key.User = 0, nil
	require.NoError(t, validateAPIKeyRouteSubscriptionIdentity(key, &User{ID: 42}, sub))
	sub.GroupID = 101
	require.ErrorIs(t, validateAPIKeyRouteSubscriptionIdentity(key, &User{ID: 42}, sub), ErrSubscriptionInvalid)
}
