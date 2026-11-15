package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// SettingPaymentTabOrder persists the recharge-centre tab layout as JSON:
//
//	{"order":["rechargeCenter","recharge"],"default":"rechargeCenter"}
//
// It uses the generic settings key-value table, next to the other payment
// settings, so no schema migration is required.
const SettingPaymentTabOrder = "payment.tabs.order.v1"

// Recharge-centre tab keys. The payment page renders exactly these tabs; the
// legacy subscription tab is only reachable through deep links and therefore is
// not part of the administrator-managed layout.
const (
	PaymentTabRechargeCenter = "rechargeCenter"
	PaymentTabRecharge       = "recharge"
)

// DefaultPaymentTabOrder mirrors the historical hard-coded tab order: the
// Alipay / WeChat recharge centre first, then the balance top-up tab.
var DefaultPaymentTabOrder = []string{PaymentTabRechargeCenter, PaymentTabRecharge}

// PaymentTabOrder is the administrator-editable recharge-centre tab layout.
type PaymentTabOrder struct {
	Order   []string `json:"order"`
	Default string   `json:"default"`
}

// defaultPaymentTabOrder returns a copy so callers can never mutate the package
// level canonical order.
func defaultPaymentTabOrder() PaymentTabOrder {
	order := make([]string, len(DefaultPaymentTabOrder))
	copy(order, DefaultPaymentTabOrder)
	return PaymentTabOrder{Order: order, Default: order[0]}
}

func isKnownPaymentTab(key string) bool {
	for _, known := range DefaultPaymentTabOrder {
		if key == known {
			return true
		}
	}
	return false
}

// buildPaymentTabOrder keeps only known, unique tab keys, appends tabs missing
// from the stored value in canonical order (so a tab is never hidden by an old
// layout) and guarantees a default that exists in the order. Empty or corrupt
// input therefore falls back to the canonical order instead of an unset state.
func buildPaymentTabOrder(order []string, def string) PaymentTabOrder {
	seen := make(map[string]struct{}, len(DefaultPaymentTabOrder))
	normalized := make([]string, 0, len(DefaultPaymentTabOrder))
	for _, key := range order {
		key = strings.TrimSpace(key)
		if !isKnownPaymentTab(key) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	for _, key := range DefaultPaymentTabOrder {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	def = strings.TrimSpace(def)
	if _, ok := seen[def]; !ok {
		def = normalized[0]
	}
	return PaymentTabOrder{Order: normalized, Default: def}
}

// sanitizePaymentTabOrder decodes a stored settings value. Invalid JSON, unknown
// tab keys and a default outside the order all fall back to safe defaults.
func sanitizePaymentTabOrder(raw string) PaymentTabOrder {
	parsed := PaymentTabOrder{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &parsed)
	}
	return buildPaymentTabOrder(parsed.Order, parsed.Default)
}

func normalizePaymentTabOrderInput(order []string, def string) (PaymentTabOrder, error) {
	if len(order) == 0 {
		return PaymentTabOrder{}, infraerrors.BadRequest(
			"PAYMENT_TAB_ORDER_INVALID",
			"payment tab order must not be empty",
		)
	}
	seen := make(map[string]struct{}, len(order))
	for _, key := range order {
		key = strings.TrimSpace(key)
		if !isKnownPaymentTab(key) {
			return PaymentTabOrder{}, infraerrors.BadRequest(
				"PAYMENT_TAB_ORDER_INVALID",
				fmt.Sprintf("unknown payment tab %q", key),
			)
		}
		if _, ok := seen[key]; ok {
			return PaymentTabOrder{}, infraerrors.BadRequest(
				"PAYMENT_TAB_ORDER_INVALID",
				fmt.Sprintf("duplicate payment tab %q", key),
			)
		}
		seen[key] = struct{}{}
	}
	def = strings.TrimSpace(def)
	if def != "" {
		if _, ok := seen[def]; !ok {
			return PaymentTabOrder{}, infraerrors.BadRequest(
				"PAYMENT_TAB_DEFAULT_INVALID",
				fmt.Sprintf("default payment tab %q is not in the order", def),
			)
		}
	}
	return buildPaymentTabOrder(order, def), nil
}

// GetPaymentTabOrder reads the administrator-managed recharge-centre tab layout.
// Missing or corrupt values always resolve to the canonical order and its first
// tab as the default.
func (s *PaymentConfigService) GetPaymentTabOrder(ctx context.Context) (PaymentTabOrder, error) {
	if s == nil || s.settingRepo == nil {
		return defaultPaymentTabOrder(), nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingPaymentTabOrder)
	if err != nil {
		return defaultPaymentTabOrder(), fmt.Errorf("get payment tab order: %w", err)
	}
	return sanitizePaymentTabOrder(raw), nil
}

// SavePaymentTabOrder validates and persists the tab layout. Unknown or
// duplicated tab keys are rejected instead of being silently dropped, so an
// administrator never sees a saved layout that differs from what was dragged.
func (s *PaymentConfigService) SavePaymentTabOrder(ctx context.Context, order []string, def string) (PaymentTabOrder, error) {
	if s == nil || s.settingRepo == nil {
		return PaymentTabOrder{}, fmt.Errorf("payment config service unavailable")
	}
	normalized, err := normalizePaymentTabOrderInput(order, def)
	if err != nil {
		return PaymentTabOrder{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return PaymentTabOrder{}, fmt.Errorf("marshal payment tab order: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingPaymentTabOrder, string(payload)); err != nil {
		return PaymentTabOrder{}, fmt.Errorf("save payment tab order: %w", err)
	}
	return normalized, nil
}
