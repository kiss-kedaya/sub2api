//go:build unit

package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func TestNormalizeVisibleMethods(t *testing.T) {
	t.Parallel()

	got := NormalizeVisibleMethods([]string{
		"alipay_direct",
		"alipay",
		" wxpay_direct ",
		"wxpay",
		"stripe",
		"ldc",
	})

	want := []string{"alipay", "wxpay", "stripe", "ldc"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeVisibleMethods len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeVisibleMethods[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestEnabledVisibleMethodsForEasyPayIncludesCustomSupportedTypes(t *testing.T) {
	t.Parallel()

	got := enabledVisibleMethodsForProvider(payment.TypeEasyPay, "alipay,ldc,usdt_trc20")
	want := []string{"alipay", "ldc", "usdt_trc20"}
	if len(got) != len(want) {
		t.Fatalf("enabledVisibleMethodsForProvider len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enabledVisibleMethodsForProvider[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestNormalizePaymentSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{name: "empty uses default", input: "", expect: PaymentSourceHostedRedirect},
		{name: "wechat alias normalized", input: "wechat_in_app", expect: PaymentSourceWechatInAppResume},
		{name: "canonical value preserved", input: PaymentSourceWechatInAppResume, expect: PaymentSourceWechatInAppResume},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizePaymentSource(tt.input); got != tt.expect {
				t.Fatalf("NormalizePaymentSource(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestCanonicalizeReturnURL(t *testing.T) {
	t.Parallel()

	got, err := CanonicalizeReturnURL("https://example.com/payment/result#a", "example.com", "")
	if err != nil {
		t.Fatalf("CanonicalizeReturnURL returned error: %v", err)
	}
	if got != "https://example.com/payment/result" {
		t.Fatalf("CanonicalizeReturnURL = %q, want %q", got, "https://example.com/payment/result")
	}
}

func TestCanonicalizeReturnURLRejectsRelativeURL(t *testing.T) {
	t.Parallel()

	if _, err := CanonicalizeReturnURL("/payment/result", "example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject relative URLs")
	}
}

func TestCanonicalizeReturnURLRejectsExternalHost(t *testing.T) {
	t.Parallel()

	if _, err := CanonicalizeReturnURL("https://evil.example/payment/result", "app.example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject external hosts")
	}
}

func TestCanonicalizeReturnURLRejectsForgedRefererHost(t *testing.T) {
	t.Parallel()

	// Client-controlled Referer must never allowlist a third-party return_url host.
	if _, err := CanonicalizeReturnURL(
		"https://evil.example/payment/result",
		"api.example.com",
		"https://evil.example/purchase",
	); err == nil {
		t.Fatal("CanonicalizeReturnURL must reject return_url host authorized only by Referer")
	}
}

func TestCanonicalizeReturnURLWithAuthoritativeOrigin(t *testing.T) {
	t.Parallel()

	// Direct IP :17777 checkout where Nginx strips the port in Host must
	// canonicalize cleanly to the authoritative frontend origin without 400.
	got, err := CanonicalizeReturnURLWithOrigin(
		"http://203.0.113.10:17777/payment/result",
		"http://203.0.113.10",
		"https://shop.example.com",
	)
	if err != nil {
		t.Fatalf("CanonicalizeReturnURLWithOrigin returned error for :17777 checkout: %v", err)
	}
	if got != "https://shop.example.com/payment/result" {
		t.Fatalf("got %q, want %q", got, "https://shop.example.com/payment/result")
	}

	// Forged host or HTTP downgrade is neutralized to authoritative origin.
	got, err = CanonicalizeReturnURLWithOrigin(
		"http://evil.example/payment/result#frag",
		"http://evil.example",
		"https://shop.example.com/v1",
	)
	if err != nil {
		t.Fatalf("CanonicalizeReturnURLWithOrigin returned error: %v", err)
	}
	if got != "https://shop.example.com/payment/result" {
		t.Fatalf("got %q, want %q", got, "https://shop.example.com/payment/result")
	}

	// Path, query, credential, and backslash guards still apply with authoritative origin.
	for _, bad := range []string{
		"https://shop.example.com/payment/result?trade_status=TRADE_SUCCESS",
		"https://shop.example.com/orders/42",
		"https://user:pass@shop.example.com/payment/result",
		`https://shop.example.com/payment\result`,
		`https://shop.example.com/payment/result\`,
	} {
		if _, err := CanonicalizeReturnURLWithOrigin(bad, "https://shop.example.com", "https://shop.example.com"); err == nil {
			t.Fatalf("expected %q to be rejected even with authoritative origin", bad)
		}
	}
}

func TestCanonicalizeReturnURLWithoutAuthoritativeOriginRequiresExactSchemeAndHost(t *testing.T) {
	t.Parallel()

	got, err := CanonicalizeReturnURLWithOrigin(
		"https://app.example.com/payment/result",
		"https://app.example.com",
		"",
	)
	if err != nil || got != "https://app.example.com/payment/result" {
		t.Fatalf("exact match failed: got=%q err=%v", got, err)
	}

	// Default port matches scheme-normalized origin.
	got, err = CanonicalizeReturnURLWithOrigin(
		"https://app.example.com:443/payment/result",
		"https://app.example.com",
		"",
	)
	if err != nil || got != "https://app.example.com:443/payment/result" {
		t.Fatalf("default port match failed: got=%q err=%v", got, err)
	}

	// HTTPS -> HTTP downgrade must be rejected.
	if _, err := CanonicalizeReturnURLWithOrigin(
		"http://app.example.com/payment/result",
		"https://app.example.com",
		"",
	); err == nil {
		t.Fatal("expected HTTPS -> HTTP downgrade to be rejected")
	}

	// Non-default port mismatch must be rejected.
	if _, err := CanonicalizeReturnURLWithOrigin(
		"http://app.example.com:17777/payment/result",
		"http://app.example.com",
		"",
	); err == nil {
		t.Fatal("expected port mismatch to be rejected")
	}
}

func TestResolveAuthoritativeReturnOriginPriorityAndNeverUsesCORS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sel := &payment.InstanceSelection{
		Config: map[string]string{
			"returnUrl": "https://provider.example/payment/result",
		},
	}

	// 1. DB frontend_url wins over everything.
	svc := &PaymentService{
		cfg: &config.Config{
			Server: config.ServerConfig{FrontendURL: "https://cfg.example"},
			CORS:   config.CORSConfig{AllowedOrigins: []string{"https://cors-must-not-be-used.example"}},
		},
		configService: &PaymentConfigService{
			settingRepo: &paymentConfigSettingRepoStub{
				values: map[string]string{
					SettingKeyFrontendURL: "https://db-frontend.example/",
					SettingKeyAPIBaseURL:  "https://db-api.example/v1",
				},
			},
		},
	}
	if got := svc.resolveAuthoritativeReturnOrigin(ctx, sel); got != "https://db-frontend.example" {
		t.Fatalf("priority 1 = %q, want https://db-frontend.example", got)
	}

	// 2. cfg.Server.FrontendURL wins when DB frontend_url is empty or invalid.
	svc.configService.settingRepo = &paymentConfigSettingRepoStub{
		values: map[string]string{
			SettingKeyFrontendURL: "javascript:alert(1)",
			SettingKeyAPIBaseURL:  "https://db-api.example/v1",
		},
	}
	if got := svc.resolveAuthoritativeReturnOrigin(ctx, sel); got != "https://cfg.example" {
		t.Fatalf("priority 2 = %q, want https://cfg.example", got)
	}

	// 3. DB api_base_url (scheme+host only) wins over provider returnUrl.
	svc.cfg.Server.FrontendURL = ""
	if got := svc.resolveAuthoritativeReturnOrigin(ctx, sel); got != "https://db-api.example" {
		t.Fatalf("priority 3 = %q, want https://db-api.example", got)
	}

	// 4. Provider instance returnUrl (scheme+host only) is last authoritative fallback.
	svc.configService.settingRepo = &paymentConfigSettingRepoStub{values: map[string]string{}}
	if got := svc.resolveAuthoritativeReturnOrigin(ctx, sel); got != "https://provider.example" {
		t.Fatalf("priority 4 = %q, want https://provider.example", got)
	}

	// 5. Never falls back to cors.allowed_origins.
	if got := svc.resolveAuthoritativeReturnOrigin(ctx, nil); got != "" {
		t.Fatalf("without authoritative sources = %q, want empty (must not use CORS)", got)
	}
}

func TestPaymentResumeTokenTTLAndMissingExpiryRejected(t *testing.T) {
	t.Parallel()

	key := []byte("0123456789abcdef0123456789abcdef")
	svc := NewPaymentResumeService(key)
	before := time.Now().Unix()
	token, err := svc.CreateToken(ResumeTokenClaims{OrderID: 42, UserID: 7})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}
	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken returned error: %v", err)
	}
	ttl := claims.ExpiresAt - before
	if ttl < int64((2*time.Hour-5*time.Second).Seconds()) || ttl > int64((2*time.Hour+5*time.Second).Seconds()) {
		t.Fatalf("resume token TTL = %ds, want ~2h (%ds)", ttl, int64((2 * time.Hour).Seconds()))
	}

	// Token with missing/zero exp must be rejected.
	zeroExpPayload, err := json.Marshal(ResumeTokenClaims{OrderID: 42, UserID: 7, IssuedAt: before})
	if err != nil {
		t.Fatalf("marshal zero exp claims: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(zeroExpPayload)
	zeroExpToken := encoded + "." + signPaymentResumePayload(encoded, key)
	if _, err := svc.ParseToken(zeroExpToken); err == nil {
		t.Fatal("ParseToken must reject tokens with missing/zero exp")
	}
}

func TestEffectiveCreateOrderIsMobileEnforcesAlipayForceQRCode(t *testing.T) {
	t.Parallel()

	officialAlipay := &payment.InstanceSelection{ProviderKey: payment.TypeAlipay}
	easyPay := &payment.InstanceSelection{ProviderKey: payment.TypeEasyPay}

	// Force QR code enabled without mobile precreate deep link forces mobile=false for Alipay.
	cfgForceQR := &PaymentConfig{AlipayForceQRCode: true}
	reqAlipayMobile := CreateOrderRequest{PaymentType: payment.TypeAlipay, IsMobile: true}
	if effectiveCreateOrderIsMobile(reqAlipayMobile, cfgForceQR, officialAlipay, false) {
		t.Fatal("expected AlipayForceQRCode to force effectiveMobile=false for official Alipay")
	}
	if effectiveCreateOrderIsMobile(reqAlipayMobile, cfgForceQR, easyPay, false) {
		t.Fatal("expected AlipayForceQRCode to force effectiveMobile=false for EasyPay Alipay")
	}

	// WeChat is unaffected by AlipayForceQRCode.
	reqWxMobile := CreateOrderRequest{PaymentType: payment.TypeWxpay, IsMobile: true}
	if !effectiveCreateOrderIsMobile(reqWxMobile, cfgForceQR, easyPay, false) {
		t.Fatal("expected AlipayForceQRCode not to affect wxpay")
	}

	// When mobile precreate deep link is enabled on official Alipay, precreate (not TradeWapPay) takes priority.
	cfgBoth := &PaymentConfig{AlipayForceQRCode: true, AlipayMobilePrecreateDeepLink: true}
	precreateOfficial := shouldUseAlipayMobilePrecreate(reqAlipayMobile, cfgBoth, officialAlipay)
	if !precreateOfficial {
		t.Fatal("expected mobile precreate enabled for official Alipay")
	}
	if !effectiveCreateOrderIsMobile(reqAlipayMobile, cfgBoth, officialAlipay, precreateOfficial) {
		t.Fatal("expected official Alipay mobile precreate to keep mobile=true so TradePreCreate runs")
	}
	// EasyPay does not support official Alipay mobile precreate, so AlipayForceQRCode still forces mobile=false.
	precreateEasyPay := shouldUseAlipayMobilePrecreate(reqAlipayMobile, cfgBoth, easyPay)
	if precreateEasyPay {
		t.Fatal("expected mobile precreate disabled for EasyPay")
	}
	if effectiveCreateOrderIsMobile(reqAlipayMobile, cfgBoth, easyPay, precreateEasyPay) {
		t.Fatal("expected AlipayForceQRCode to force effectiveMobile=false for EasyPay even when precreate toggle is on")
	}
}

func TestPaymentRedirectPathFromURLRejectsBackslash(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{
		`/\evil.example`,
		`/purchase\evil`,
		`https://shop.invalid/\evil.example`,
		`https://shop.invalid/purchase?next=\evil`,
		`//evil.example/purchase`,
		"/purchase\r\nX: 1",
	} {
		if got := paymentRedirectPathFromURL(bad); got != "/purchase" {
			t.Fatalf("paymentRedirectPathFromURL(%q) = %q, want /purchase", bad, got)
		}
	}
}

func TestCanonicalizeReturnURLRejectsNonCanonicalPath(t *testing.T) {
	t.Parallel()

	if _, err := CanonicalizeReturnURL("https://app.example.com/orders/42", "app.example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject non-canonical result paths")
	}
}

func TestBuildPaymentReturnURL(t *testing.T) {
	t.Parallel()

	got, err := buildPaymentReturnURL("https://example.com/payment/result#fragment", 42, "sub2_42", "resume-token")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsed.Fragment != "" {
		t.Fatalf("buildPaymentReturnURL should strip fragments, got %q", parsed.Fragment)
	}
	query := parsed.Query()
	if query.Get("from") != "" {
		t.Fatalf("unexpected client-controlled query, got %q", query.Get("from"))
	}
	if query.Get("order_id") != strconv.FormatInt(42, 10) {
		t.Fatalf("order_id = %q", query.Get("order_id"))
	}
	if query.Get("out_trade_no") != "sub2_42" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("resume_token") != "resume-token" {
		t.Fatalf("resume_token = %q", query.Get("resume_token"))
	}
	if query.Get("status") != "success" {
		t.Fatalf("status = %q", query.Get("status"))
	}
}

func TestBuildPaymentReturnURLWithoutResumeTokenStillIncludesOutTradeNo(t *testing.T) {
	t.Parallel()

	got, err := buildPaymentReturnURL("https://example.com/payment/result", 42, "sub2_42", "")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	query := parsed.Query()
	if query.Get("order_id") != "42" {
		t.Fatalf("order_id = %q", query.Get("order_id"))
	}
	if query.Get("out_trade_no") != "sub2_42" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("resume_token") != "" {
		t.Fatalf("resume_token = %q, want empty", query.Get("resume_token"))
	}
}

func TestBuildPaymentReturnURLEmptyBase(t *testing.T) {
	t.Parallel()

	got, err := buildPaymentReturnURL("", 42, "sub2_42", "resume-token")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("buildPaymentReturnURL = %q, want empty string", got)
	}
}

func TestPaymentResumeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	svc := NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateToken(ResumeTokenClaims{
		OrderID:            42,
		UserID:             7,
		ProviderInstanceID: "19",
		ProviderKey:        "easypay",
		PaymentType:        "wxpay",
		CanonicalReturnURL: "https://example.com/payment/result",
		IssuedAt:           1234567890,
	})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}

	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken returned error: %v", err)
	}
	if claims.OrderID != 42 || claims.UserID != 7 {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.ProviderInstanceID != "19" || claims.ProviderKey != "easypay" || claims.PaymentType != "wxpay" {
		t.Fatalf("claims provider snapshot mismatch: %+v", claims)
	}
	if claims.CanonicalReturnURL != "https://example.com/payment/result" {
		t.Fatalf("claims return URL = %q", claims.CanonicalReturnURL)
	}
}

func TestCreateTokenRejectsMissingSigningKey(t *testing.T) {
	t.Parallel()

	svc := NewPaymentResumeService(nil)
	_, err := svc.CreateToken(ResumeTokenClaims{OrderID: 42})
	if err == nil {
		t.Fatal("CreateToken should reject missing signing key")
	}
}

func TestParseTokenRejectsFallbackSignedTokenWhenSigningKeyMissing(t *testing.T) {
	t.Parallel()

	token := mustCreateFallbackSignedToken(t, ResumeTokenClaims{OrderID: 42, UserID: 7})
	svc := NewPaymentResumeService(nil)
	_, err := svc.ParseToken(token)
	if err == nil {
		t.Fatal("ParseToken should reject tokens when signing key is missing")
	}
}

func TestParseTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	svc := NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateToken(ResumeTokenClaims{
		OrderID:   42,
		UserID:    7,
		IssuedAt:  time.Now().Add(-25 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}

	_, err = svc.ParseToken(token)
	if err == nil {
		t.Fatal("ParseToken should reject expired tokens")
	}
}

func TestWeChatPaymentResumeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	svc := NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      7,
		RedirectTo:  "/purchase?from=wechat",
		Scope:       "snsapi_base",
		IssuedAt:    1234567890,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-123" || claims.PaymentType != payment.TypeWxpay {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.Amount != "12.50" || claims.OrderType != payment.OrderTypeSubscription || claims.PlanID != 7 {
		t.Fatalf("claims payment context mismatch: %+v", claims)
	}
	if claims.RedirectTo != "/purchase?from=wechat" || claims.Scope != "snsapi_base" {
		t.Fatalf("claims redirect/scope mismatch: %+v", claims)
	}
}

func TestCreateWeChatPaymentResumeTokenRejectsMissingSigningKey(t *testing.T) {
	t.Parallel()

	svc := NewPaymentResumeService(nil)
	_, err := svc.CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{OpenID: "openid-123"})
	if err == nil {
		t.Fatal("CreateWeChatPaymentResumeToken should reject missing signing key")
	}
}

func TestParseWeChatPaymentResumeTokenRejectsFallbackSignedTokenWhenSigningKeyMissing(t *testing.T) {
	t.Parallel()

	token := mustCreateFallbackSignedToken(t, WeChatPaymentResumeClaims{
		TokenType:   wechatPaymentResumeTokenType,
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
	})
	svc := NewPaymentResumeService(nil)
	_, err := svc.ParseWeChatPaymentResumeToken(token)
	if err == nil {
		t.Fatal("ParseWeChatPaymentResumeToken should reject tokens when signing key is missing")
	}
}

func TestParseWeChatPaymentResumeTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	svc := NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		IssuedAt:    time.Now().Add(-30 * time.Minute).Unix(),
		ExpiresAt:   time.Now().Add(-1 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	_, err = svc.ParseWeChatPaymentResumeToken(token)
	if err == nil {
		t.Fatal("ParseWeChatPaymentResumeToken should reject expired tokens")
	}
}

func TestPaymentServiceParseWeChatPaymentResumeTokenUsesExplicitSigningKey(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "explicit-payment-resume-signing-key")

	token, err := NewPaymentResumeService([]byte("explicit-payment-resume-signing-key")).CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{
		OpenID:      "openid-explicit-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	svc := &PaymentService{
		configService: &PaymentConfigService{
			encryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		},
	}

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-explicit-key" {
		t.Fatalf("openid = %q, want %q", claims.OpenID, "openid-explicit-key")
	}
}

func TestPaymentServiceParseWeChatPaymentResumeTokenAcceptsLegacyEncryptionKeyDuringMigration(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "explicit-payment-resume-signing-key")

	legacyKey := []byte("0123456789abcdef0123456789abcdef")
	token, err := NewPaymentResumeService(legacyKey).CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{
		OpenID:      "openid-legacy-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	svc := &PaymentService{
		configService: &PaymentConfigService{
			encryptionKey: legacyKey,
		},
	}

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-legacy-key" {
		t.Fatalf("openid = %q, want %q", claims.OpenID, "openid-legacy-key")
	}
}

func TestNewConfiguredPaymentResumeServicePrefersExplicitSigningKeyAndKeepsLegacyVerificationFallback(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "explicit-payment-resume-signing-key")

	legacyKey := []byte("0123456789abcdef0123456789abcdef")
	svc := newLegacyAwarePaymentResumeService(legacyKey)

	explicitToken, err := svc.CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{
		OpenID:      "openid-explicit-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	explicitClaims, err := NewPaymentResumeService([]byte("explicit-payment-resume-signing-key")).ParseWeChatPaymentResumeToken(explicitToken)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if explicitClaims.OpenID != "openid-explicit-key" {
		t.Fatalf("openid = %q, want %q", explicitClaims.OpenID, "openid-explicit-key")
	}

	legacyToken, err := NewPaymentResumeService(legacyKey).CreateWeChatPaymentResumeToken(WeChatPaymentResumeClaims{
		OpenID:      "openid-legacy-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	legacyClaims, err := svc.ParseWeChatPaymentResumeToken(legacyToken)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if legacyClaims.OpenID != "openid-legacy-key" {
		t.Fatalf("openid = %q, want %q", legacyClaims.OpenID, "openid-legacy-key")
	}
}

func TestNormalizeVisibleMethodSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		input  string
		want   string
	}{
		{name: "alipay official alias", method: payment.TypeAlipay, input: "alipay", want: VisibleMethodSourceOfficialAlipay},
		{name: "alipay easypay alias", method: payment.TypeAlipay, input: "easypay", want: VisibleMethodSourceEasyPayAlipay},
		{name: "wxpay official alias", method: payment.TypeWxpay, input: "wxpay", want: VisibleMethodSourceOfficialWechat},
		{name: "wxpay easypay alias", method: payment.TypeWxpay, input: "easypay", want: VisibleMethodSourceEasyPayWechat},
		{name: "unsupported source", method: payment.TypeWxpay, input: "stripe", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeVisibleMethodSource(tt.method, tt.input); got != tt.want {
				t.Fatalf("NormalizeVisibleMethodSource(%q, %q) = %q, want %q", tt.method, tt.input, got, tt.want)
			}
		})
	}
}

func TestVisibleMethodProviderKeyForSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		source string
		want   string
		ok     bool
	}{
		{name: "official alipay", method: payment.TypeAlipay, source: VisibleMethodSourceOfficialAlipay, want: payment.TypeAlipay, ok: true},
		{name: "easypay alipay", method: payment.TypeAlipay, source: VisibleMethodSourceEasyPayAlipay, want: payment.TypeEasyPay, ok: true},
		{name: "official wechat", method: payment.TypeWxpay, source: VisibleMethodSourceOfficialWechat, want: payment.TypeWxpay, ok: true},
		{name: "easypay wechat", method: payment.TypeWxpay, source: VisibleMethodSourceEasyPayWechat, want: payment.TypeEasyPay, ok: true},
		{name: "mismatched method and source", method: payment.TypeAlipay, source: VisibleMethodSourceOfficialWechat, want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := VisibleMethodProviderKeyForSource(tt.method, tt.source)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("VisibleMethodProviderKeyForSource(%q, %q) = (%q, %v), want (%q, %v)", tt.method, tt.source, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestVisibleMethodLoadBalancerUsesEnabledProviderInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Official Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(1).
		Save(ctx)
	if err != nil {
		t.Fatalf("create alipay provider: %v", err)
	}

	inner := &captureLoadBalancer{}
	configService := &PaymentConfigService{
		entClient: client,
	}
	lb := newVisibleMethodLoadBalancer(inner, configService)

	_, err = lb.SelectInstance(ctx, "", payment.TypeAlipay, payment.StrategyRoundRobin, 12.5)
	if err != nil {
		t.Fatalf("SelectInstance returned error: %v", err)
	}
	if inner.lastProviderKey != payment.TypeAlipay {
		t.Fatalf("lastProviderKey = %q, want %q", inner.lastProviderKey, payment.TypeAlipay)
	}
}

func TestVisibleMethodLoadBalancerUsesConfiguredSourceWhenMultipleProvidersEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		method        payment.PaymentType
		officialName  string
		officialTypes string
		easyPayName   string
		easyPayTypes  string
		sourceSetting string
		wantProvider  string
	}{
		{
			name:          "alipay uses official source",
			method:        payment.TypeAlipay,
			officialName:  "Official Alipay",
			officialTypes: "alipay",
			easyPayName:   "EasyPay Alipay",
			easyPayTypes:  "alipay",
			sourceSetting: VisibleMethodSourceOfficialAlipay,
			wantProvider:  payment.TypeAlipay,
		},
		{
			name:          "alipay uses easypay source",
			method:        payment.TypeAlipay,
			officialName:  "Official Alipay",
			officialTypes: "alipay",
			easyPayName:   "EasyPay Alipay",
			easyPayTypes:  "alipay",
			sourceSetting: VisibleMethodSourceEasyPayAlipay,
			wantProvider:  payment.TypeEasyPay,
		},
		{
			name:          "wxpay uses official source",
			method:        payment.TypeWxpay,
			officialName:  "Official WeChat",
			officialTypes: "wxpay",
			easyPayName:   "EasyPay WeChat",
			easyPayTypes:  "wxpay",
			sourceSetting: VisibleMethodSourceOfficialWechat,
			wantProvider:  payment.TypeWxpay,
		},
		{
			name:          "wxpay uses easypay source",
			method:        payment.TypeWxpay,
			officialName:  "Official WeChat",
			officialTypes: "wxpay",
			easyPayName:   "EasyPay WeChat",
			easyPayTypes:  "wxpay",
			sourceSetting: VisibleMethodSourceEasyPayWechat,
			wantProvider:  payment.TypeEasyPay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)

			officialProviderKey := payment.TypeAlipay
			if tt.method == payment.TypeWxpay {
				officialProviderKey = payment.TypeWxpay
			}

			_, err := client.PaymentProviderInstance.Create().
				SetProviderKey(officialProviderKey).
				SetName(tt.officialName).
				SetConfig("{}").
				SetSupportedTypes(tt.officialTypes).
				SetEnabled(true).
				SetSortOrder(1).
				Save(ctx)
			if err != nil {
				t.Fatalf("create official provider: %v", err)
			}

			_, err = client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeEasyPay).
				SetName(tt.easyPayName).
				SetConfig("{}").
				SetSupportedTypes(tt.easyPayTypes).
				SetEnabled(true).
				SetSortOrder(2).
				Save(ctx)
			if err != nil {
				t.Fatalf("create easypay provider: %v", err)
			}

			inner := &captureLoadBalancer{}
			configService := &PaymentConfigService{
				entClient: client,
				settingRepo: &paymentConfigSettingRepoStub{
					values: map[string]string{
						visibleMethodSourceSettingKey(tt.method): tt.sourceSetting,
					},
				},
			}
			lb := newVisibleMethodLoadBalancer(inner, configService)

			_, err = lb.SelectInstance(ctx, "", tt.method, payment.StrategyRoundRobin, 12.5)
			if err != nil {
				t.Fatalf("SelectInstance returned error: %v", err)
			}
			if inner.lastProviderKey != tt.wantProvider {
				t.Fatalf("lastProviderKey = %q, want %q", inner.lastProviderKey, tt.wantProvider)
			}
		})
	}
}

func TestVisibleMethodLoadBalancerPreservesLegacyCrossProviderRoutingWhenSourceMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Official Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(1).
		Save(ctx)
	if err != nil {
		t.Fatalf("create official provider: %v", err)
	}

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(2).
		Save(ctx)
	if err != nil {
		t.Fatalf("create easypay provider: %v", err)
	}

	inner := &captureLoadBalancer{}
	configService := &PaymentConfigService{
		entClient: client,
		settingRepo: &paymentConfigSettingRepoStub{
			values: map[string]string{
				visibleMethodSourceSettingKey(payment.TypeAlipay): "",
			},
		},
	}
	lb := newVisibleMethodLoadBalancer(inner, configService)

	_, err = lb.SelectInstance(ctx, "", payment.TypeAlipay, payment.StrategyRoundRobin, 9.9)
	if err != nil {
		t.Fatalf("SelectInstance returned error: %v", err)
	}
	if inner.lastProviderKey != "" {
		t.Fatalf("lastProviderKey = %q, want legacy cross-provider empty key", inner.lastProviderKey)
	}
	if inner.lastPaymentType != payment.TypeAlipay {
		t.Fatalf("lastPaymentType = %q, want %q", inner.lastPaymentType, payment.TypeAlipay)
	}
}

func TestVisibleMethodLoadBalancerRejectsInvalidSourceWhenMultipleProvidersEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      payment.PaymentType
		sourceValue string
		wantMessage string
	}{
		{
			name:        "invalid wxpay source",
			method:      payment.TypeWxpay,
			sourceValue: "stripe",
			wantMessage: "wxpay source must be one of the supported payment providers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)

			officialProviderKey := payment.TypeAlipay
			officialSupportedTypes := "alipay"
			officialName := "Official Alipay"
			easyPaySupportedTypes := "alipay"
			easyPayName := "EasyPay Alipay"
			if tt.method == payment.TypeWxpay {
				officialProviderKey = payment.TypeWxpay
				officialSupportedTypes = "wxpay"
				officialName = "Official WeChat"
				easyPaySupportedTypes = "wxpay"
				easyPayName = "EasyPay WeChat"
			}

			_, err := client.PaymentProviderInstance.Create().
				SetProviderKey(officialProviderKey).
				SetName(officialName).
				SetConfig("{}").
				SetSupportedTypes(officialSupportedTypes).
				SetEnabled(true).
				SetSortOrder(1).
				Save(ctx)
			if err != nil {
				t.Fatalf("create official provider: %v", err)
			}

			_, err = client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeEasyPay).
				SetName(easyPayName).
				SetConfig("{}").
				SetSupportedTypes(easyPaySupportedTypes).
				SetEnabled(true).
				SetSortOrder(2).
				Save(ctx)
			if err != nil {
				t.Fatalf("create easypay provider: %v", err)
			}

			inner := &captureLoadBalancer{}
			configService := &PaymentConfigService{
				entClient: client,
				settingRepo: &paymentConfigSettingRepoStub{
					values: map[string]string{
						visibleMethodSourceSettingKey(tt.method): tt.sourceValue,
					},
				},
			}
			lb := newVisibleMethodLoadBalancer(inner, configService)

			_, err = lb.SelectInstance(ctx, "", tt.method, payment.StrategyRoundRobin, 9.9)
			if err == nil {
				t.Fatal("SelectInstance should reject invalid visible method source configuration")
			}
			if infraerrors.Reason(err) != "INVALID_PAYMENT_VISIBLE_METHOD_SOURCE" {
				t.Fatalf("Reason(err) = %q, want %q", infraerrors.Reason(err), "INVALID_PAYMENT_VISIBLE_METHOD_SOURCE")
			}
			if infraerrors.Message(err) != tt.wantMessage {
				t.Fatalf("Message(err) = %q, want %q", infraerrors.Message(err), tt.wantMessage)
			}
		})
	}
}

func TestVisibleMethodLoadBalancerRejectsMissingEnabledVisibleMethodProvider(t *testing.T) {
	t.Parallel()

	inner := &captureLoadBalancer{}
	configService := &PaymentConfigService{
		entClient: newPaymentConfigServiceTestClient(t),
	}
	lb := newVisibleMethodLoadBalancer(inner, configService)

	if _, err := lb.SelectInstance(context.Background(), "", payment.TypeWxpay, payment.StrategyRoundRobin, 9.9); err == nil {
		t.Fatal("SelectInstance should reject when no enabled provider instance exists")
	}
}

type captureLoadBalancer struct {
	lastProviderKey string
	lastPaymentType string
}

func (c *captureLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	return map[string]string{}, nil
}

func (c *captureLoadBalancer) SelectInstance(_ context.Context, providerKey string, paymentType payment.PaymentType, _ payment.Strategy, _ float64) (*payment.InstanceSelection, error) {
	c.lastProviderKey = providerKey
	c.lastPaymentType = paymentType
	return &payment.InstanceSelection{ProviderKey: providerKey, SupportedTypes: paymentType}, nil
}

func mustCreateFallbackSignedToken(t *testing.T, claims any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte("sub2api-payment-resume"))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}
