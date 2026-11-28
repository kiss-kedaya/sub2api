//go:build unit

package service

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveRebateRatePercent_PerUserOverride verifies that per-inviter
// AffRebateRatePercent overrides the global rate, that NULL falls back to the
// global rate, and that out-of-range exclusive rates are clamped silently.
//
// SettingService is left nil here so globalRebateRatePercent returns the
// documented default (AffiliateRebateRateDefault = 20%) — this exercises the
// fallback path without spinning up a settings stub.
func TestResolveRebateRatePercent_PerUserOverride(t *testing.T) {
	t.Parallel()
	svc := &AffiliateService{}

	// nil exclusive rate → falls back to global default (20%)
	require.InDelta(t, AffiliateRebateRateDefault,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{}), 1e-9)

	// exclusive rate set → overrides global
	rate := 50.0
	require.InDelta(t, 50.0,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &rate}), 1e-9)

	// exclusive rate 0 → returns 0 (no rebate, intentional)
	zero := 0.0
	require.InDelta(t, 0.0,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &zero}), 1e-9)

	// exclusive rate above max → clamped to Max
	tooHigh := 250.0
	require.InDelta(t, AffiliateRebateRateMax,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &tooHigh}), 1e-9)

	// exclusive rate below min → clamped to Min
	tooLow := -5.0
	require.InDelta(t, AffiliateRebateRateMin,
		svc.resolveRebateRatePercent(context.Background(), &AffiliateSummary{AffRebateRatePercent: &tooLow}), 1e-9)
}

// TestIsEnabled_NilSettingServiceReturnsDefault verifies that IsEnabled
// safely handles a nil settingService dependency by returning the default
// (off). This protects callers from nil-pointer crashes in misconfigured
// environments.
func TestIsEnabled_NilSettingServiceReturnsDefault(t *testing.T) {
	t.Parallel()
	svc := &AffiliateService{}
	require.False(t, svc.IsEnabled(context.Background()))
	require.Equal(t, AffiliateEnabledDefault, svc.IsEnabled(context.Background()))
}

// TestValidateExclusiveRate_BoundaryAndInvalid covers the validator used by
// admin-facing rate setters: nil is always valid (clear), in-range values
// are accepted, NaN/Inf and out-of-range values produce a typed BadRequest.
func TestValidateExclusiveRate_BoundaryAndInvalid(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateExclusiveRate(nil))

	for _, v := range []float64{0, 0.01, 50, 99.99, 100} {
		v := v
		require.NoError(t, validateExclusiveRate(&v), "value %v should be valid", v)
	}

	for _, v := range []float64{-0.01, 100.01, -100, 200} {
		v := v
		require.Error(t, validateExclusiveRate(&v), "value %v should be rejected", v)
	}

	nan := math.NaN()
	require.Error(t, validateExclusiveRate(&nan))
	posInf := math.Inf(1)
	require.Error(t, validateExclusiveRate(&posInf))
	negInf := math.Inf(-1)
	require.Error(t, validateExclusiveRate(&negInf))
}

func TestMaskEmail(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a***@g***.com", maskEmail("alice@gmail.com"))
	require.Equal(t, "x***@d***", maskEmail("x@domain"))
	require.Equal(t, "", maskEmail(""))
}

func TestIsValidAffiliateCodeFormat(t *testing.T) {
	t.Parallel()

	// 邀请码格式校验同时服务于：
	// 1) 系统自动生成的 12 位随机码（A-Z 去 I/O，2-9 去 0/1）
	// 2) 管理员设置的自定义专属码（如 "VIP2026"、"NEW_USER-1"）
	// 因此校验放宽到 [A-Z0-9_-]{4,32}（要求调用方先 ToUpper）。
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid canonical 12-char", "ABCDEFGHJKLM", true},
		{"valid all digits 2-9", "234567892345", true},
		{"valid mixed", "A2B3C4D5E6F7", true},
		{"valid admin custom short", "VIP1", true},
		{"valid admin custom with hyphen", "NEW-USER", true},
		{"valid admin custom with underscore", "VIP_2026", true},
		{"valid 32-char max", "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", true},
		// Previously-excluded chars (I/O/0/1) are now allowed since admins may use them.
		{"letter I now allowed", "IBCDEFGHJKLM", true},
		{"letter O now allowed", "OBCDEFGHJKLM", true},
		{"digit 0 now allowed", "0BCDEFGHJKLM", true},
		{"digit 1 now allowed", "1BCDEFGHJKLM", true},
		{"too short (3 chars)", "ABC", false},
		{"too long (33 chars)", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456", false},
		{"lowercase rejected (caller must ToUpper first)", "abcdefghjklm", false},
		{"empty", "", false},
		{"utf8 non-ascii", "ÄÄÄÄÄÄ", false}, // bytes out of charset
		{"ascii punctuation .", "ABCDEFGHJK.M", false},
		{"whitespace", "ABCDEFGHJK M", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isValidAffiliateCodeFormat(tc.in))
		})
	}
}

type batchRateRecordingAffiliateRepo struct {
	*paymentFulfillmentAffiliateRepoStub
	ids []int64
}

func (r *batchRateRecordingAffiliateRepo) BatchSetUserRebateRate(_ context.Context, ids []int64, _ *float64) error {
	r.ids = append([]int64(nil), ids...)
	return nil
}

func TestAdminBatchSetUserRebateRateCountDeduplicatesInvalidIDs(t *testing.T) {
	repo := &batchRateRecordingAffiliateRepo{paymentFulfillmentAffiliateRepoStub: &paymentFulfillmentAffiliateRepoStub{}}
	svc := NewAffiliateService(repo, nil, nil, nil)
	rate := 25.0
	affected, err := svc.AdminBatchSetUserRebateRateCount(context.Background(), []int64{7, 7, 0, -3, 9}, &rate)
	require.NoError(t, err)
	require.Equal(t, 2, affected)
	require.Equal(t, []int64{7, 9}, repo.ids)
}

type atomicSettingsRecordingAffiliateRepo struct {
	*paymentFulfillmentAffiliateRepoStub
	updateCalls int
}

func (r *atomicSettingsRecordingAffiliateRepo) UpdateUserSettings(_ context.Context, _ int64, _ *string, _ *float64, _ bool) error {
	r.updateCalls++
	return nil
}

func (r *atomicSettingsRecordingAffiliateRepo) ClearUserSettings(context.Context, int64) error {
	return nil
}

func TestAdminUpdateUserSettingsValidatesBeforeAtomicWrite(t *testing.T) {
	repo := &atomicSettingsRecordingAffiliateRepo{paymentFulfillmentAffiliateRepoStub: &paymentFulfillmentAffiliateRepoStub{}}
	svc := NewAffiliateService(repo, nil, nil, nil)
	badRate := math.NaN()
	code := "VIP2026"
	require.Error(t, svc.AdminUpdateUserSettings(context.Background(), 7, &code, &badRate, false))
	require.Zero(t, repo.updateCalls)
	rate := 12.5
	require.NoError(t, svc.AdminUpdateUserSettings(context.Background(), 7, &code, &rate, false))
	require.Equal(t, 1, repo.updateCalls)
}

func TestApplyAffiliateCodeStatsKeepsRetiredLinks(t *testing.T) {
	codes := []UserAffiliateCode{{ID: 7, Code: "vip-1"}}
	stats := map[string]AffiliateCodeStat{
		"DEFAULT1": {InviteCount: 2, RebateAmount: 1.5},
		"VIP-1":    {InviteCount: 3, RebateAmount: 4},
		"OLDCODE":  {InviteCount: 1, RebateAmount: 0.25},
	}
	count, rebate, out := applyAffiliateCodeStats("default1", codes, stats)
	require.Equal(t, 2, count)
	require.InDelta(t, 1.5, rebate, 1e-9)
	require.Len(t, out, 2)
	require.Equal(t, "VIP-1", out[0].Code)
	require.Equal(t, 3, out[0].InviteCount)
	require.InDelta(t, 4, out[0].RebateAmount, 1e-9)
	require.False(t, out[0].Retired)
	require.Equal(t, int64(0), out[1].ID)
	require.Equal(t, "OLDCODE", out[1].Code)
	require.True(t, out[1].Retired)
	require.Equal(t, 1, out[1].InviteCount)
}

type cappedAccrualRecordingAffiliateRepo struct {
	*paymentFulfillmentAffiliateRepoStub
	inviterID           int64
	cappedCalls         int
	lastAmount          float64
	lastCap             float64
	legacyAccrueCalls   int
	legacyCapQueryCalls int
	returnApplied       float64
	reverseCalls        int
	lastReverseOrderID  int64
	lastReverseRatio    float64
	reverseReturnAmount float64
}

func (r *cappedAccrualRecordingAffiliateRepo) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	if userID == r.inviterID {
		return &AffiliateSummary{UserID: r.inviterID}, nil
	}
	inviterID := r.inviterID
	return &AffiliateSummary{UserID: userID, InviterID: &inviterID}, nil
}

func (r *cappedAccrualRecordingAffiliateRepo) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	r.legacyCapQueryCalls++
	return 0, nil
}

func (r *cappedAccrualRecordingAffiliateRepo) AccrueQuota(context.Context, int64, int64, float64, int, *int64) (bool, error) {
	r.legacyAccrueCalls++
	return true, nil
}

func (r *cappedAccrualRecordingAffiliateRepo) AccrueQuotaWithCap(_ context.Context, _, _ int64, amount float64, _ int, _ *int64, perInviteeCap float64) (float64, error) {
	r.cappedCalls++
	r.lastAmount = amount
	r.lastCap = perInviteeCap
	return r.returnApplied, nil
}

func (r *cappedAccrualRecordingAffiliateRepo) ReverseQuotaForOrder(_ context.Context, sourceOrderID int64, ratio float64) (float64, error) {
	r.reverseCalls++
	r.lastReverseOrderID = sourceOrderID
	r.lastReverseRatio = ratio
	return r.reverseReturnAmount, nil
}

func TestAccrueInviteRebateForOrderUsesAtomicCapRepositoryMethod(t *testing.T) {
	settingsRepo := &stubSettingsMapRepo{values: map[string]string{
		SettingKeyAffiliateEnabled:             "true",
		SettingKeyAffiliateRebateRate:          "20",
		SettingKeyAffiliateRebatePerInviteeCap: "15",
	}}
	settingSvc := NewSettingService(settingsRepo, nil)
	repo := &cappedAccrualRecordingAffiliateRepo{
		paymentFulfillmentAffiliateRepoStub: &paymentFulfillmentAffiliateRepoStub{},
		inviterID:                           7,
		returnApplied:                       15,
	}
	svc := NewAffiliateService(repo, settingSvc, nil, nil)
	orderID := int64(99)

	rebate, err := svc.AccrueInviteRebateForOrder(context.Background(), 8, 100, &orderID)
	require.NoError(t, err)
	require.InDelta(t, 15.0, rebate, 1e-9)
	require.Equal(t, 1, repo.cappedCalls, "must use atomic AccrueQuotaWithCap")
	require.InDelta(t, 20.0, repo.lastAmount, 1e-9)
	require.InDelta(t, 15.0, repo.lastCap, 1e-9)
	require.Zero(t, repo.legacyCapQueryCalls, "must not query per-invitee cap outside tx")
	require.Zero(t, repo.legacyAccrueCalls)
}

type legacyOnlyCapAffiliateRepo struct {
	*paymentFulfillmentAffiliateRepoStub
	inviterID   int64
	existing    float64
	accrueCalls int
	lastAmount  float64
}

func (r *legacyOnlyCapAffiliateRepo) EnsureUserAffiliate(_ context.Context, userID int64) (*AffiliateSummary, error) {
	if userID == r.inviterID {
		return &AffiliateSummary{UserID: r.inviterID}, nil
	}
	inviterID := r.inviterID
	return &AffiliateSummary{UserID: userID, InviterID: &inviterID}, nil
}

func (r *legacyOnlyCapAffiliateRepo) GetAccruedRebateFromInvitee(context.Context, int64, int64) (float64, error) {
	return r.existing, nil
}

func (r *legacyOnlyCapAffiliateRepo) AccrueQuota(_ context.Context, _, _ int64, amount float64, _ int, _ *int64) (bool, error) {
	r.accrueCalls++
	r.lastAmount = amount
	return true, nil
}

func TestAccrueInviteRebateFallbackRoundsCapRemainderDown(t *testing.T) {
	settingsRepo := &stubSettingsMapRepo{values: map[string]string{
		SettingKeyAffiliateEnabled:             "true",
		SettingKeyAffiliateRebateRate:          "20",
		SettingKeyAffiliateRebatePerInviteeCap: "1.000000006",
	}}
	settingSvc := NewSettingService(settingsRepo, nil)
	repo := &legacyOnlyCapAffiliateRepo{
		paymentFulfillmentAffiliateRepoStub: &paymentFulfillmentAffiliateRepoStub{},
		inviterID:                           7,
		existing:                            1.0,
	}
	svc := NewAffiliateService(repo, settingSvc, nil, nil)
	orderID := int64(101)

	rebate, err := svc.AccrueInviteRebateForOrder(context.Background(), 8, 100, &orderID)
	require.NoError(t, err)
	require.Zero(t, rebate, "sub-unit remainder 0.000000006 must truncate downward to 0")
	require.Zero(t, repo.accrueCalls)
}

func TestReverseInviteRebateForOrderRunsEvenWhenAffiliateDisabled(t *testing.T) {
	repo := &cappedAccrualRecordingAffiliateRepo{
		paymentFulfillmentAffiliateRepoStub: &paymentFulfillmentAffiliateRepoStub{},
		reverseReturnAmount:                 6.5,
	}
	// Nil settingService => IsEnabled == false; reversal must still execute.
	svc := NewAffiliateService(repo, nil, nil, nil)

	reversed, err := svc.ReverseInviteRebateForOrder(context.Background(), 42, 1.0)
	require.NoError(t, err)
	require.InDelta(t, 6.5, reversed, 1e-9)
	require.Equal(t, 1, repo.reverseCalls)
	require.Equal(t, int64(42), repo.lastReverseOrderID)
	require.InDelta(t, 1.0, repo.lastReverseRatio, 1e-9)

	// Invalid ratio (NaN / Inf / <= 0) is rejected as no-op.
	for _, bad := range []float64{0, -0.5, math.NaN(), math.Inf(1)} {
		got, err := svc.ReverseInviteRebateForOrder(context.Background(), 42, bad)
		require.NoError(t, err)
		require.Zero(t, got)
	}
	require.Equal(t, 1, repo.reverseCalls)
}

type stubSettingsMapRepo struct {
	values map[string]string
}

func (r *stubSettingsMapRepo) Get(_ context.Context, key string) (*Setting, error) {
	v, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: v}, nil
}
func (r *stubSettingsMapRepo) GetValue(_ context.Context, key string) (string, error) {
	v, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}
func (r *stubSettingsMapRepo) Set(context.Context, string, string) error { return nil }
func (r *stubSettingsMapRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := r.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}
func (r *stubSettingsMapRepo) SetMultiple(context.Context, map[string]string) error { return nil }
func (r *stubSettingsMapRepo) GetAll(context.Context) (map[string]string, error) {
	return r.values, nil
}
func (r *stubSettingsMapRepo) Delete(context.Context, string) error { return nil }
