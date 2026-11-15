package service

import (
	"context"
	"encoding/json"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// paymentTabOrderRepo implements only the settings accessors used by the tab
// layout; the embedded interface keeps the test independent of the ent layer.
type paymentTabOrderRepo struct {
	SettingRepository
	values map[string]string
}

func newPaymentTabOrderRepo() *paymentTabOrderRepo {
	return &paymentTabOrderRepo{values: map[string]string{}}
}

func (r *paymentTabOrderRepo) GetValue(_ context.Context, key string) (string, error) {
	return r.values[key], nil
}

func (r *paymentTabOrderRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *paymentTabOrderRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = r.values[key]
	}
	return result, nil
}

func (r *paymentTabOrderRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func newPaymentTabOrderService(repo *paymentTabOrderRepo) *PaymentConfigService {
	return &PaymentConfigService{settingRepo: repo}
}

func TestPaymentTabOrderDefaultsToCanonicalOrderAndFirstTab(t *testing.T) {
	svc := newPaymentTabOrderService(newPaymentTabOrderRepo())

	layout, err := svc.GetPaymentTabOrder(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{PaymentTabRechargeCenter, PaymentTabRecharge}, layout.Order)
	require.Equal(t, PaymentTabRechargeCenter, layout.Default)

	// The parsed payment config exposes the same resolved layout to every
	// authenticated reader (GET /payment/config, checkout-info).
	cfg := svc.parsePaymentConfig(map[string]string{})
	require.Equal(t, layout.Order, cfg.TabOrder)
	require.Equal(t, layout.Default, cfg.DefaultTab)
}

func TestPaymentTabOrderSanitizesCorruptAndUnknownValues(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantOrder   []string
		wantDefault string
	}{
		{
			name:        "invalid json falls back to canonical order",
			raw:         "{not json",
			wantOrder:   []string{PaymentTabRechargeCenter, PaymentTabRecharge},
			wantDefault: PaymentTabRechargeCenter,
		},
		{
			name:        "unknown and duplicate keys are dropped",
			raw:         `{"order":["wxpay","recharge","wxpay"],"default":"wxpay"}`,
			wantOrder:   []string{PaymentTabRecharge, PaymentTabRechargeCenter},
			wantDefault: PaymentTabRecharge,
		},
		{
			name:        "missing known tab is appended",
			raw:         `{"order":["recharge"],"default":"recharge"}`,
			wantOrder:   []string{PaymentTabRecharge, PaymentTabRechargeCenter},
			wantDefault: PaymentTabRecharge,
		},
		{
			name:        "default outside the order falls back to the first tab",
			raw:         `{"order":["recharge","rechargeCenter"],"default":"subscription"}`,
			wantOrder:   []string{PaymentTabRecharge, PaymentTabRechargeCenter},
			wantDefault: PaymentTabRecharge,
		},
		{
			name:        "empty order falls back to canonical order",
			raw:         `{"order":[],"default":""}`,
			wantOrder:   []string{PaymentTabRechargeCenter, PaymentTabRecharge},
			wantDefault: PaymentTabRechargeCenter,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layout := sanitizePaymentTabOrder(tc.raw)
			require.Equal(t, tc.wantOrder, layout.Order)
			require.Equal(t, tc.wantDefault, layout.Default)
		})
	}
}

func TestPaymentTabOrderSavePersistsAndRoundTrips(t *testing.T) {
	repo := newPaymentTabOrderRepo()
	svc := newPaymentTabOrderService(repo)

	saved, err := svc.SavePaymentTabOrder(context.Background(), []string{PaymentTabRecharge, PaymentTabRechargeCenter}, PaymentTabRecharge)
	require.NoError(t, err)
	require.Equal(t, []string{PaymentTabRecharge, PaymentTabRechargeCenter}, saved.Order)
	require.Equal(t, PaymentTabRecharge, saved.Default)

	raw := repo.values[SettingPaymentTabOrder]
	require.NotEmpty(t, raw)
	var stored PaymentTabOrder
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, saved, stored)

	layout, err := svc.GetPaymentTabOrder(context.Background())
	require.NoError(t, err)
	require.Equal(t, saved, layout)
}

func TestPaymentTabOrderUpdatePaymentConfigPersistsLayout(t *testing.T) {
	repo := newPaymentTabOrderRepo()
	svc := newPaymentTabOrderService(repo)
	order := []string{PaymentTabRecharge, PaymentTabRechargeCenter}
	def := PaymentTabRecharge

	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		TabOrder:   &order,
		TabDefault: &def,
	}))

	layout, err := svc.GetPaymentTabOrder(context.Background())
	require.NoError(t, err)
	require.Equal(t, order, layout.Order)
	require.Equal(t, def, layout.Default)

	// A default-only patch must keep the stored order untouched.
	newDefault := PaymentTabRechargeCenter
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		TabDefault: &newDefault,
	}))
	layout, err = svc.GetPaymentTabOrder(context.Background())
	require.NoError(t, err)
	require.Equal(t, order, layout.Order)
	require.Equal(t, newDefault, layout.Default)
}

func TestPaymentTabOrderRejectsInvalidWrites(t *testing.T) {
	cases := []struct {
		name       string
		order      []string
		def        string
		wantReason string
	}{
		{name: "empty order", order: nil, def: "", wantReason: "PAYMENT_TAB_ORDER_INVALID"},
		{name: "unknown tab", order: []string{"alipay", PaymentTabRecharge}, def: "alipay", wantReason: "PAYMENT_TAB_ORDER_INVALID"},
		{name: "duplicate tab", order: []string{PaymentTabRecharge, PaymentTabRecharge}, def: PaymentTabRecharge, wantReason: "PAYMENT_TAB_ORDER_INVALID"},
		{name: "default outside order", order: []string{PaymentTabRecharge}, def: "subscription", wantReason: "PAYMENT_TAB_DEFAULT_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newPaymentTabOrderRepo()
			svc := newPaymentTabOrderService(repo)
			_, err := svc.SavePaymentTabOrder(context.Background(), tc.order, tc.def)
			require.Error(t, err)
			require.True(t, infraerrors.IsBadRequest(err))
			require.Equal(t, tc.wantReason, infraerrors.Reason(err))
			require.Empty(t, repo.values[SettingPaymentTabOrder], "invalid layouts must not be persisted")
		})
	}
}
