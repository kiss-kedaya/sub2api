//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentBalanceRedeemUsesNormalCacheInvalidationAndOutboxPath(t *testing.T) {
	for _, codeType := range []string{RedeemTypePaymentBalance, RedeemTypeBalance} {
		t.Run(codeType, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			userID := int64(42)
			code := &RedeemCode{ID: 910, Code: "PAYMENT-BALANCE-COMPAT", Type: codeType, Value: 24.5, Status: StatusUnused}
			redeemRepo := &paymentFulfillmentRedeemRepo{
				paymentOrderLifecycleRedeemRepo: paymentOrderLifecycleRedeemRepo{
					codesByCode: map[string]*RedeemCode{code.Code: code},
				},
			}
			userRepo := &mockUserRepo{getByIDUser: &User{ID: userID}}
			var credited float64
			userRepo.updateBalanceFn = func(_ context.Context, gotUserID int64, amount float64) error {
				require.Equal(t, userID, gotUserID)
				credited += amount
				return nil
			}
			liveCache := &liveBalanceAdjustmentCacheStub{}
			invalidator := &authCacheInvalidatorStub{}
			svc := NewRedeemService(
				redeemRepo, userRepo, nil, nil, &BillingCacheService{cache: liveCache}, client, invalidator, nil,
			)

			got, err := svc.redeemForPaymentFulfillment(ctx, userID, code.Code)

			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, StatusUsed, got.Status)
			require.Equal(t, userID, *got.UsedBy)
			require.Equal(t, 24.5, credited)
			require.Equal(t, 1, liveCache.invalidateCalls, "clear the legacy balance cache after the transaction commits")
			require.Zero(t, liveCache.adjustCalls, "the durable database outbox remains the only persistent live-wallet delta writer")
			require.Zero(t, liveCache.watermarkCalls, "the request path must not apply the outbox event directly")
			require.Equal(t, []int64{userID}, invalidator.userIDs, "balance auth cache must be invalidated")
			require.Len(t, redeemRepo.useCalls, 1)

			_, err = svc.redeemForPaymentFulfillment(ctx, userID, code.Code)
			require.ErrorIs(t, err, ErrRedeemCodeUsed)
			require.Equal(t, 1, liveCache.invalidateCalls, "duplicate fulfillment must not repeat cache synchronization")
			require.Equal(t, []int64{userID}, invalidator.userIDs, "duplicate fulfillment must not repeat auth invalidation")
			require.Equal(t, 1, len(redeemRepo.useCalls))
		})
	}
}

func TestPaymentBalanceRedeemDoesNotInvalidateCachesWhenBalanceUpdateFails(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	userID := int64(42)
	code := &RedeemCode{ID: 911, Code: "PAYMENT-BALANCE-FAILED", Type: RedeemTypePaymentBalance, Value: 24.5, Status: StatusUnused}
	redeemRepo := &paymentFulfillmentRedeemRepo{
		paymentOrderLifecycleRedeemRepo: paymentOrderLifecycleRedeemRepo{
			codesByCode: map[string]*RedeemCode{code.Code: code},
		},
	}
	userRepo := &mockUserRepo{getByIDUser: &User{ID: userID}}
	updateErr := errors.New("synthetic balance update failure")
	userRepo.updateBalanceFn = func(context.Context, int64, float64) error { return updateErr }
	liveCache := &liveBalanceAdjustmentCacheStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := NewRedeemService(
		redeemRepo, userRepo, nil, nil, &BillingCacheService{cache: liveCache}, client, invalidator, nil,
	)

	got, err := svc.redeemForPaymentFulfillment(ctx, userID, code.Code)

	require.Nil(t, got)
	require.ErrorIs(t, err, updateErr)
	require.Zero(t, liveCache.invalidateCalls)
	require.Zero(t, liveCache.adjustCalls)
	require.Empty(t, invalidator.userIDs)
}

func TestPaymentAndOrdinaryBalanceTypesShareBalanceInvalidation(t *testing.T) {
	require.True(t, isBalanceRedeemType(RedeemTypeBalance))
	require.True(t, isBalanceRedeemType(RedeemTypePaymentBalance))
	require.False(t, isBalanceRedeemType(RedeemTypeConcurrency))
	require.False(t, isBalanceRedeemType(RedeemTypeSubscription))

	invalidator := &authCacheInvalidatorStub{}
	svc := &RedeemService{authCacheInvalidator: invalidator}
	svc.invalidateRedeemCaches(context.Background(), 77, &RedeemCode{Type: RedeemTypePaymentBalance})
	require.Equal(t, []int64{77}, invalidator.userIDs)
}
