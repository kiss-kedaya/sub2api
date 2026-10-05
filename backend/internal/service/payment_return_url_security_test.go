package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPaymentReturnURLRejectsUserQueryParameters(t *testing.T) {
	for _, suffix := range []string{
		"?trade_no=POC-FAKE-TRADE&trade_status=TRADE_SUCCESS",
		"?trade_status=TRADE_SUCCESS&trade_no=fixture",
		"?%74rade_status=TRADE_SUCCESS", "?resume_token=fixture", "?out_trade_no=fixture",
		"?from=checkout", "?", "?bad=%zz", "?a=1&trade_status=WAITING&trade_status=TRADE_SUCCESS",
	} {
		t.Run(suffix, func(t *testing.T) {
			raw := "https://shop.invalid/payment/result" + suffix
			_, err := CanonicalizeReturnURL(raw, "shop.invalid", "")
			require.Error(t, err)
			_, err = buildPaymentReturnURL(raw, 1, "fixture-order", "fixture-resume-token")
			require.Error(t, err, "old or unvalidated resume URLs must not bypass the guard")
		})
	}
	_, err := CanonicalizeReturnURL("https://user:secret@shop.invalid/payment/result", "shop.invalid", "")
	require.Error(t, err)
	_, err = CanonicalizeReturnURL("https://shop.invalid/payment%2fresult", "shop.invalid", "")
	require.Error(t, err)
}

func TestPaymentReturnURLValidationDoesNotNeedProviderOrOrder(t *testing.T) {
	// Exercise the pure guard separately from order creation; no signed payment
	// URLs, user balances or provider requests are needed to reject the input.
	canonical, err := CanonicalizeReturnURL("https://shop.invalid/payment/result", "shop.invalid", "")
	require.NoError(t, err)
	result, err := buildPaymentReturnURL(canonical, 1, "fixture-order", "fixture-token")
	require.NoError(t, err)
	require.Contains(t, result, "order_id=1")
	require.Contains(t, result, "out_trade_no=fixture-order")
	require.Contains(t, result, "resume_token=fixture-token")
	require.NotContains(t, result, "trade_status=")
}
