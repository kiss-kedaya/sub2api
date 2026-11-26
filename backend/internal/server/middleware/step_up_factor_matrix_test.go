package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// factorStepUpStub 同时实现按因子校验的 grantChecker 与因子判定入口，
// 用来覆盖「只绑 TOTP / 只绑 Passkey / 两者都绑 / 都没有」的 step-up 矩阵。
type factorStepUpStub struct {
	required  []service.Factor
	granted   map[service.Factor]bool
	expired   map[service.Factor]bool
	factorErr error
	grantErr  map[service.Factor]error
	remaining map[service.Factor]time.Duration
}

func (s factorStepUpStub) RequiredFactors(context.Context, int64) ([]service.Factor, error) {
	if s.factorErr != nil {
		return nil, s.factorErr
	}
	return s.required, nil
}

func (s factorStepUpStub) HasStepUpGrant(_ context.Context, _ int64, _ string, factor service.Factor) (bool, error) {
	if err := s.grantErr[factor]; err != nil {
		return false, err
	}
	return s.granted[factor], nil
}

func (s factorStepUpStub) StepUpGrantRemaining(_ context.Context, _ int64, _ string, factor service.Factor) (time.Duration, bool, error) {
	if s.expired[factor] {
		return 0, false, nil
	}
	if remaining, ok := s.remaining[factor]; ok {
		return remaining, true, nil
	}
	return 4 * time.Minute, true, nil
}

func TestEnforceStepUpFactorMatrix(t *testing.T) {
	totpUser := &service.User{ID: 1, TotpEnabled: true}
	passkeyOnlyUser := &service.User{ID: 2}
	bothUser := &service.User{ID: 3, TotpEnabled: true}
	noFactorUser := &service.User{ID: 4}

	cases := []struct {
		name     string
		user     *service.User
		stub     factorStepUpStub
		wantPass bool
		wantCode string
	}{
		{
			name:     "totp only granted",
			user:     totpUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorTOTP}, granted: map[service.Factor]bool{service.FactorTOTP: true}},
			wantPass: true,
		},
		{
			name:     "passkey only granted",
			user:     passkeyOnlyUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorPasskey}, granted: map[service.Factor]bool{service.FactorPasskey: true}},
			wantPass: true,
		},
		{
			name:     "passkey only missing",
			user:     passkeyOnlyUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorPasskey}, granted: map[service.Factor]bool{service.FactorTOTP: true}},
			wantCode: "STEP_UP_REQUIRED",
		},
		{
			name:     "both granted",
			user:     bothUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorTOTP, service.FactorPasskey}, granted: map[service.Factor]bool{service.FactorTOTP: true, service.FactorPasskey: true}},
			wantPass: true,
		},
		// 两者都绑定：任选其一即可（老板 2026-11-26）。只有一个因子有授权也放行。
		{
			name:     "both bound, only totp granted",
			user:     bothUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorTOTP, service.FactorPasskey}, granted: map[service.Factor]bool{service.FactorTOTP: true}},
			wantPass: true,
		},
		{
			name:     "both bound, only passkey granted",
			user:     bothUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorTOTP, service.FactorPasskey}, granted: map[service.Factor]bool{service.FactorPasskey: true}},
			wantPass: true,
		},
		{
			name:     "both bound, none granted",
			user:     bothUser,
			stub:     factorStepUpStub{required: []service.Factor{service.FactorTOTP, service.FactorPasskey}},
			wantCode: "STEP_UP_REQUIRED",
		},
		{
			name:     "no factor enrolled",
			user:     noFactorUser,
			stub:     factorStepUpStub{required: nil},
			wantCode: "STEP_UP_TOTP_NOT_ENABLED",
		},
		{
			name:     "policy read failure fails closed",
			user:     bothUser,
			stub:     factorStepUpStub{factorErr: errors.New("db down")},
			wantCode: "STEP_UP_UNAVAILABLE",
		},
		{
			name: "grant read failure fails closed",
			user: bothUser,
			stub: factorStepUpStub{
				required: []service.Factor{service.FactorTOTP},
				grantErr: map[service.Factor]error{service.FactorTOTP: errors.New("redis down")},
			},
			wantCode: "STEP_UP_UNAVAILABLE",
		},
		{
			name: "window expired between reads",
			user: totpUser,
			stub: factorStepUpStub{
				required: []service.Factor{service.FactorTOTP},
				granted:  map[service.Factor]bool{service.FactorTOTP: true},
				expired:  map[service.Factor]bool{service.FactorTOTP: true},
			},
			wantCode: "STEP_UP_REQUIRED",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newStepUpTestContext(t)
			c.Set(string(ContextKeyUser), AuthSubject{UserID: tc.user.ID})

			ok := enforceStepUp(c, tc.stub, stubStepUpUserReader{user: tc.user}, stepUpEnabled)
			if tc.wantPass {
				require.True(t, ok)
				require.False(t, c.IsAborted())
				return
			}
			require.False(t, ok)
			require.Contains(t, rec.Body.String(), tc.wantCode)
		})
	}
}

// 两者都有授权时：X-StepUp-Factors 回传全部满足的因子；任选其一，所以免验证窗口取
// 最晚过期的那个授权（最早的过期后只要还有一个有效授权就仍然免验证）。
func TestEnforceStepUpBothFactorsReportsHeaders(t *testing.T) {
	c, rec := newStepUpTestContext(t)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})

	stub := factorStepUpStub{
		required: []service.Factor{service.FactorTOTP, service.FactorPasskey},
		granted:  map[service.Factor]bool{service.FactorTOTP: true, service.FactorPasskey: true},
		remaining: map[service.Factor]time.Duration{
			service.FactorTOTP:    280 * time.Second,
			service.FactorPasskey: 140 * time.Second,
		},
	}
	ok := enforceStepUp(c, stub, stubStepUpUserReader{user: &service.User{ID: 3, TotpEnabled: true}}, stepUpEnabled)
	require.True(t, ok)
	require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
	// 取较大（最晚过期）的窗口：280 秒。
	require.Equal(t, "280", rec.Header().Get(StepUpRemainingHeader))
	require.Equal(t, "totp,passkey", rec.Header().Get(StepUpFactorsHeader))
}

// 只有一个因子满足时：X-StepUp-Factors 只回传那个因子，剩余时间就是它自己的。
func TestEnforceStepUpSingleSatisfiedFactorReportsHeaders(t *testing.T) {
	for _, tc := range []struct {
		name        string
		granted     service.Factor
		wantFactors string
	}{
		{"totp only", service.FactorTOTP, "totp"},
		{"passkey only", service.FactorPasskey, "passkey"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := newStepUpTestContext(t)
			c.Set(string(ContextKeyUser), AuthSubject{UserID: 3})

			stub := factorStepUpStub{
				required:  []service.Factor{service.FactorTOTP, service.FactorPasskey},
				granted:   map[service.Factor]bool{tc.granted: true},
				remaining: map[service.Factor]time.Duration{tc.granted: 100 * time.Second},
			}
			ok := enforceStepUp(c, stub, stubStepUpUserReader{user: &service.User{ID: 3, TotpEnabled: true}}, stepUpEnabled)
			require.True(t, ok)
			require.Equal(t, "1", rec.Header().Get(StepUpSkippedHeader))
			require.Equal(t, "100", rec.Header().Get(StepUpRemainingHeader))
			require.Equal(t, tc.wantFactors, rec.Header().Get(StepUpFactorsHeader))
		})
	}
}
