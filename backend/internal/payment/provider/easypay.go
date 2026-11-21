// Package provider contains concrete payment provider implementations.
package provider

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// EasyPay constants.
const (
	easypayCodeSuccess     = 1
	easypayStatusPaid      = 1
	easypayHTTPTimeout     = 10 * time.Second
	maxEasypayResponseSize = 1 << 20 // 1MB
	maxEasypayErrorSummary = 512
	tradeStatusSuccess     = "TRADE_SUCCESS"
	signTypeMD5            = "MD5"
	paymentModePopup       = "popup"
	deviceMobile           = "mobile"
)

// EasyPay implements payment.Provider for the EasyPay aggregation platform.
type EasyPay struct {
	instanceID string
	config     map[string]string
	httpClient *http.Client
}

type easyPayCustomMethod struct {
	Type         string `json:"type"`
	UpstreamType string `json:"upstreamType"`
	DisplayName  string `json:"displayName"`
}

// NewEasyPay creates a new EasyPay provider.
// config keys: pid, pkey, apiBase, notifyUrl, returnUrl, cid, cidAlipay, cidWxpay
func NewEasyPay(instanceID string, config map[string]string) (*EasyPay, error) {
	for _, k := range []string{"pid", "pkey", "apiBase", "notifyUrl", "returnUrl"} {
		if strings.TrimSpace(config[k]) == "" {
			return nil, fmt.Errorf("easypay config missing required key: %s", k)
		}
	}
	cfg := make(map[string]string, len(config))
	for k, v := range config {
		cfg[k] = v
	}
	cfg["apiBase"] = normalizeEasyPayAPIBase(cfg["apiBase"])
	return &EasyPay{
		instanceID: instanceID,
		config:     cfg,
		httpClient: &http.Client{Timeout: easypayHTTPTimeout},
	}, nil
}

func normalizeEasyPayAPIBase(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return ""
	}
	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.RawPath = ""
		parsed.Path = trimEasyPayEndpointPath(parsed.Path)
		return strings.TrimRight(parsed.String(), "/")
	}
	return strings.TrimRight(trimEasyPayEndpointPath(base), "/")
}

func trimEasyPayEndpointPath(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	lower := strings.ToLower(path)
	for _, endpoint := range []string{"/submit.php", "/mapi.php", "/api.php"} {
		if strings.HasSuffix(lower, endpoint) {
			return strings.TrimRight(path[:len(path)-len(endpoint)], "/")
		}
	}
	return path
}

func (e *EasyPay) apiBase() string {
	if e == nil {
		return ""
	}
	return normalizeEasyPayAPIBase(e.config["apiBase"])
}

func (e *EasyPay) Name() string        { return "EasyPay" }
func (e *EasyPay) ProviderKey() string { return payment.TypeEasyPay }
func (e *EasyPay) SupportedTypes() []payment.PaymentType {
	types := []payment.PaymentType{payment.TypeAlipay, payment.TypeWxpay}
	for _, method := range e.customMethods() {
		if method.Type != "" {
			types = append(types, method.Type)
		}
	}
	return types
}

func (e *EasyPay) MerchantIdentityMetadata() map[string]string {
	if e == nil {
		return nil
	}
	pid := strings.TrimSpace(e.config["pid"])
	if pid == "" {
		return nil
	}
	return map[string]string{"pid": pid}
}

func (e *EasyPay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	// Payment mode determined by instance config, not payment type.
	// "popup" → hosted page (submit.php); "qrcode"/default → API call (mapi.php).
	mode := e.config["paymentMode"]
	if mode == paymentModePopup {
		return e.createRedirectPayment(req)
	}
	return e.createAPIPayment(ctx, req)
}

// createRedirectPayment builds a submit.php URL for browser redirect.
// No server-side API call — the user is redirected to EasyPay's hosted page.
// TradeNo is empty; it arrives via the notify callback after payment.
func (e *EasyPay) createRedirectPayment(req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	notifyURL, returnURL := e.resolveURLs(req)
	paymentType := e.upstreamPaymentType(req.PaymentType)
	params := map[string]string{
		"pid": e.config["pid"], "type": paymentType,
		"out_trade_no": req.OrderID, "notify_url": notifyURL,
		"return_url": returnURL, "name": req.Subject,
		"money": req.Amount,
	}
	if cid := e.resolveCID(paymentType); cid != "" {
		params["cid"] = cid
	}
	if req.IsMobile {
		params["device"] = deviceMobile
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	payURL := e.apiBase() + "/submit.php?" + q.Encode()
	return &payment.CreatePaymentResponse{PayURL: payURL}, nil
}

// createAPIPayment calls mapi.php to get payurl/qrcode (existing behavior).
func (e *EasyPay) createAPIPayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	notifyURL, returnURL := e.resolveURLs(req)
	paymentType := e.upstreamPaymentType(req.PaymentType)
	params := map[string]string{
		"pid": e.config["pid"], "type": paymentType,
		"out_trade_no": req.OrderID, "notify_url": notifyURL,
		"return_url": returnURL, "name": req.Subject,
		"money": req.Amount, "clientip": req.ClientIP,
	}
	if cid := e.resolveCID(paymentType); cid != "" {
		params["cid"] = cid
	}
	if req.IsMobile {
		params["device"] = deviceMobile
	}
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5

	body, err := e.post(ctx, e.apiBase()+"/mapi.php", params)
	if err != nil {
		return nil, fmt.Errorf("easypay create: %w", err)
	}
	var resp struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		TradeNo string `json:"trade_no"`
		PayURL  string `json:"payurl"`
		PayURL2 string `json:"payurl2"` // H5 mobile payment URL
		QRCode  string `json:"qrcode"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("easypay parse: %w", err)
	}
	if resp.Code != easypayCodeSuccess {
		return nil, fmt.Errorf("easypay error: %s", resp.Msg)
	}
	payURL := resp.PayURL
	if req.IsMobile && resp.PayURL2 != "" {
		payURL = resp.PayURL2
	}
	base := e.apiBase()
	return &payment.CreatePaymentResponse{
		TradeNo: resp.TradeNo,
		PayURL:  resolveEasyPayReturnedRef(base, payURL),
		QRCode:  resolveEasyPayReturnedRef(base, resp.QRCode),
	}, nil
}

// resolveEasyPayReturnedRef absolutizes a payurl/payurl2/qrcode reference that
// mapi.php returned, resolving it against the instance's configured apiBase.
//
// EasyPay-compatible upstreams disagree on this field: some answer with a full
// checkout URL, others with a site-root-relative path such as
// "/api/pay/toapp/<order>". createRedirectPayment already builds an absolute URL
// itself (apiBase + "/submit.php?..."), but the mapi.php branch stored whatever
// came back verbatim, and nothing downstream repairs it —
// sanitizeCreatePaymentResponseDetails only strips NUL bytes before the value is
// persisted to pay_url/qr_code. The frontend then feeds qr_code straight into
// QRCode.toCanvas (PaymentQRCodeView.renderQR), so a relative path becomes a QR
// whose payload is a bare path: WeChat renders it as text, and pay_url resolves
// against the gateway's own domain and 404s.
//
// That is precisely the outcome the Alipay provider already refuses to produce —
// "Setting it as QRCode would let the frontend render an unscannable image"
// (createPagePayTrade) — so EasyPay is brought in line with the same rule.
//
// Only references beginning with "/" are resolved. Anything carrying a scheme is
// already actionable and is returned untouched, which covers both absolute
// https:// checkout URLs and app deep links (weixin://, wxp://, alipays://). A
// reference without a leading slash is left alone as well: QR payloads are
// frequently opaque tokens rather than paths, and rewriting "OrderToken123" into
// "<apiBase>/OrderToken123" would break a payload that works today — strictly
// worse than the bug this fixes.
func resolveEasyPayReturnedRef(apiBase, ref string) string {
	trimmed := strings.TrimSpace(ref)
	if !strings.HasPrefix(trimmed, "/") {
		return ref
	}
	base, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ref
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "" {
		return ref
	}
	return base.ResolveReference(parsed).String()
}

// resolveURLs returns (notifyURL, returnURL) preferring request values,
// falling back to instance config.
func (e *EasyPay) resolveURLs(req payment.CreatePaymentRequest) (string, string) {
	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = e.config["notifyUrl"]
	}
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = e.config["returnUrl"]
	}
	return notifyURL, returnURL
}

func (e *EasyPay) customMethods() []easyPayCustomMethod {
	if e == nil {
		return nil
	}
	raw := strings.TrimSpace(e.config["customMethods"])
	if raw == "" {
		return nil
	}
	var methods []easyPayCustomMethod
	if err := json.Unmarshal([]byte(raw), &methods); err != nil {
		return nil
	}
	result := make([]easyPayCustomMethod, 0, len(methods))
	for _, method := range methods {
		method.Type = strings.TrimSpace(method.Type)
		method.UpstreamType = strings.TrimSpace(method.UpstreamType)
		method.DisplayName = strings.TrimSpace(method.DisplayName)
		if method.Type == "" || method.UpstreamType == "" {
			continue
		}
		result = append(result, method)
	}
	return result
}

func (e *EasyPay) upstreamPaymentType(paymentType string) string {
	paymentType = strings.TrimSpace(paymentType)
	for _, method := range e.customMethods() {
		if paymentType == method.Type {
			return method.UpstreamType
		}
	}
	return paymentType
}

func (e *EasyPay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	params := map[string]string{
		"act": "order", "pid": e.config["pid"],
		"key": e.config["pkey"], "out_trade_no": tradeNo,
	}
	// The documented order query is a GET with query-string parameters
	// (api.php?act=order&pid=..&key=..&out_trade_no=..). Sending it as a form
	// POST made the upstream ignore act/out_trade_no and answer a failure, which
	// surfaced as "easypay verification: upstream query failed" on every paid
	// callback -- money in, credit never granted.
	body, httpStatus, err := e.getRaw(ctx, e.apiBase()+"/api.php", params)
	if err != nil {
		return nil, fmt.Errorf("easypay query: %w", err)
	}
	if httpStatus < http.StatusOK || httpStatus >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("easypay query failed (HTTP %d)", httpStatus)
	}
	type easyPayQueryData struct {
		OutTradeNo  *string `json:"out_trade_no"`
		TradeStatus *string `json:"trade_status"`
		Status      *int    `json:"status"`
		Money       *string `json:"money"`
		TradeNo     *string `json:"trade_no"`
	}
	var resp struct {
		Code        int              `json:"code"`
		Msg         string           `json:"msg"`
		OutTradeNo  *string          `json:"out_trade_no"`
		TradeStatus *string          `json:"trade_status"`
		Status      *int             `json:"status"`
		Money       *string          `json:"money"`
		TradeNo     *string          `json:"trade_no"`
		Data        easyPayQueryData `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("easypay parse query: %w", err)
	}
	if resp.Code != easypayCodeSuccess {
		return nil, fmt.Errorf("easypay query was not successful")
	}
	for _, upstreamOrder := range []*string{resp.OutTradeNo, resp.Data.OutTradeNo} {
		if upstreamOrder != nil && strings.TrimSpace(*upstreamOrder) != "" && strings.TrimSpace(*upstreamOrder) != strings.TrimSpace(tradeNo) {
			return nil, fmt.Errorf("easypay query returned a different out_trade_no")
		}
	}
	status := payment.ProviderStatusPending
	if resp.TradeStatus != nil {
		if *resp.TradeStatus == tradeStatusSuccess {
			status = payment.ProviderStatusPaid
		}
	} else if resp.Data.TradeStatus != nil {
		if *resp.Data.TradeStatus == tradeStatusSuccess {
			status = payment.ProviderStatusPaid
		}
	} else if resp.Status != nil {
		if *resp.Status == easypayStatusPaid {
			status = payment.ProviderStatusPaid
		}
	} else if resp.Data.Status != nil && *resp.Data.Status == easypayStatusPaid {
		status = payment.ProviderStatusPaid
	}

	money := ""
	if resp.Money != nil {
		money = *resp.Money
	} else if resp.Data.Money != nil {
		money = *resp.Data.Money
	}
	// Do not substitute out_trade_no for an absent gateway trade_no. Callers
	// must be able to distinguish a real upstream transaction identifier.
	responseTradeNo := ""
	if resp.TradeNo != nil && strings.TrimSpace(*resp.TradeNo) != "" {
		responseTradeNo = strings.TrimSpace(*resp.TradeNo)
	} else if resp.Data.TradeNo != nil && strings.TrimSpace(*resp.Data.TradeNo) != "" {
		responseTradeNo = *resp.Data.TradeNo
	}

	amount, _ := strconv.ParseFloat(money, 64)
	return &payment.QueryOrderResponse{
		TradeNo:  responseTradeNo,
		Status:   status,
		Amount:   amount,
		Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

func (e *EasyPay) VerifyNotification(_ context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("parse notify: %w", err)
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return nil, fmt.Errorf("duplicate notify parameter %q", key)
		}
	}
	// url.ParseQuery already decodes values — no additional decode needed.
	//
	// Fields are deliberately NOT allowlisted by name: EasyPay-compatible
	// gateways disagree on the exact callback field set (bill_trade_no,
	// api_trade_no, buyer, addtime/endtime, ...), and rejecting an unknown key
	// stranded genuinely paid orders forever (the notify always 400'd, so the
	// provider kept retrying and the order stayed PENDING). Instead every
	// non-empty field participates in the MD5 sign base string, exactly like the
	// gateway computes it, and the business subset we actually read is required
	// to be present. A provider-generated field can therefore only ever appear
	// if the gateway signed it, and it cannot change routing, amount or status
	// because those are read from fixed keys below.
	params := make(map[string]string, len(values))
	for k := range values {
		value := values.Get(k)
		// Upstream #7881: the MD5 base string concatenates values unescaped, so
		// one signed value can be re-cut into several top-level parameters
		// ("&trade_status=TRADE_SUCCESS") while the signature still matches. The
		// replay can only be built from a signed checkout URL, so it always
		// reuses the checkout request. Two guards below fail closed on that
		// shape, without rejecting provider-generated fields.
		if easyPayNotifyRejectedCarrierParams[k] {
			return nil, fmt.Errorf("order-creation-only notify parameter: %s", k)
		}
		if easyPayNotifyValueCarriesParamPair(value) {
			return nil, fmt.Errorf("notify parameter %q embeds a serialized parameter pair", k)
		}
		params[k] = value
	}
	// The fields below are the only ones the business layer trusts. Any missing
	// one means the callback cannot be attributed or settled, so reject it
	// instead of guessing (a paid order is still recoverable through the
	// upstream QueryOrder reconcile path).
	for _, key := range easyPayNotifyRequiredParams {
		if strings.TrimSpace(params[key]) == "" {
			return nil, fmt.Errorf("missing notify param: %s", key)
		}
	}
	if !easyPayVerifySign(params, e.config["pkey"], params["sign"]) {
		return nil, fmt.Errorf("invalid signature")
	}
	money, err := strconv.ParseFloat(strings.TrimSpace(params["money"]), 64)
	if err != nil || math.IsNaN(money) || math.IsInf(money, 0) {
		return nil, fmt.Errorf("invalid notify money: %q", params["money"])
	}
	// Only the exact success token counts. Every other value (TRADE_CLOSED,
	// TRADE_FINISHED, missing, ...) is reported as failed so the caller ignores
	// it without touching the order. The order amount itself is compared by the
	// service layer against payment_orders.pay_amount before any credit.
	status := payment.ProviderStatusFailed
	if params["trade_status"] == tradeStatusSuccess {
		status = payment.ProviderStatusSuccess
	}

	metadata := e.MerchantIdentityMetadata()
	if pid := strings.TrimSpace(params["pid"]); pid != "" {
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["pid"] = pid
	}
	return &payment.PaymentNotification{
		TradeNo: params["trade_no"], OrderID: params["out_trade_no"],
		Amount: money, Status: status, RawData: rawBody, Metadata: metadata,
	}, nil
}

func (e *EasyPay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	attempts := e.refundAttempts(req)
	if len(attempts) == 0 {
		return nil, fmt.Errorf("easypay refund missing order identifier")
	}
	var firstErr error
	for i, attempt := range attempts {
		body, status, err := e.postRaw(ctx, e.apiBase()+"/api.php?act=refund", attempt.params)
		if err != nil {
			return nil, fmt.Errorf("easypay refund request: %w", err)
		}
		if err := parseEasyPayRefundResponse(status, body); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if i+1 < len(attempts) && isEasyPayRefundOrderNotFound(err) {
				continue
			}
			return nil, err
		}
		return &payment.RefundResponse{RefundID: attempt.refundID, Status: payment.ProviderStatusSuccess}, nil
	}
	return nil, firstErr
}

type easyPayRefundAttempt struct {
	params   map[string]string
	refundID string
}

func (e *EasyPay) refundAttempts(req payment.RefundRequest) []easyPayRefundAttempt {
	base := map[string]string{
		"pid": e.config["pid"], "key": e.config["pkey"], "money": req.Amount,
	}
	var attempts []easyPayRefundAttempt
	if orderID := strings.TrimSpace(req.OrderID); orderID != "" {
		params := cloneStringMap(base)
		params["out_trade_no"] = orderID
		attempts = append(attempts, easyPayRefundAttempt{params: params, refundID: orderID})
	}
	if tradeNo := strings.TrimSpace(req.TradeNo); tradeNo != "" {
		params := cloneStringMap(base)
		params["trade_no"] = tradeNo
		attempts = append(attempts, easyPayRefundAttempt{params: params, refundID: tradeNo})
	}
	return attempts
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func isEasyPayRefundOrderNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	return strings.Contains(msg, "订单编号不存在") ||
		strings.Contains(msg, "订单不存在") ||
		strings.Contains(lower, "order not found") ||
		strings.Contains(lower, "not exist")
}

func parseEasyPayRefundResponse(status int, body []byte) error {
	summary := summarizeEasyPayResponse(body)
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("easypay refund HTTP %d: %s", status, summary)
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return fmt.Errorf("easypay refund empty response (HTTP %d): %s", status, summary)
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") ||
		(strings.HasPrefix(lower, "<") && strings.Contains(lower, "html")) {
		return fmt.Errorf("easypay refund non-JSON response (HTTP %d): %s", status, summary)
	}

	var resp struct {
		Code any    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("easypay refund non-JSON response (HTTP %d): %s", status, summary)
	}
	if !easyPayResponseCodeIsSuccess(resp.Code) {
		msg := strings.TrimSpace(resp.Msg)
		if msg == "" {
			msg = summary
		}
		return fmt.Errorf("easypay refund failed (HTTP %d): %s", status, msg)
	}
	return nil
}

func easyPayResponseCodeIsSuccess(code any) bool {
	switch v := code.(type) {
	case float64:
		return int(v) == easypayCodeSuccess
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return err == nil && n == easypayCodeSuccess
	default:
		return false
	}
}

func summarizeEasyPayResponse(body []byte) string {
	summary := strings.Join(strings.Fields(string(body)), " ")
	if summary == "" {
		return "<empty>"
	}
	if len(summary) > maxEasypayErrorSummary {
		truncated := summary[:maxEasypayErrorSummary]
		for len(truncated) > 0 && !utf8.ValidString(truncated) {
			truncated = truncated[:len(truncated)-1]
		}
		return truncated + "..."
	}
	return summary
}

func (e *EasyPay) resolveCID(paymentType string) string {
	if strings.HasPrefix(paymentType, "alipay") {
		if v := e.config["cidAlipay"]; v != "" {
			return v
		}
		return e.config["cid"]
	}
	if v := e.config["cidWxpay"]; v != "" {
		return v
	}
	return e.config["cid"]
}

func (e *EasyPay) post(ctx context.Context, endpoint string, params map[string]string) ([]byte, error) {
	body, _, err := e.postRaw(ctx, endpoint, params)
	return body, err
}

func (e *EasyPay) postRaw(ctx context.Context, endpoint string, params map[string]string) ([]byte, int, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := e.httpClient
	if client == nil {
		client = &http.Client{Timeout: easypayHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEasypayResponseSize))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// getRaw issues a GET with the parameters encoded in the query string. EasyPay's
// merchant APIs are documented that way and do not read a POST body.
func (e *EasyPay) getRaw(ctx context.Context, endpoint string, params map[string]string) ([]byte, int, error) {
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	client := e.httpClient
	if client == nil {
		client = &http.Client{Timeout: easypayHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEasypayResponseSize))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func easyPaySign(params map[string]string, pkey string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for i, k := range keys {
		if i > 0 {
			_ = buf.WriteByte('&')
		}
		_, _ = buf.WriteString(k + "=" + params[k])
	}
	_, _ = buf.WriteString(pkey)
	hash := md5.Sum([]byte(buf.String()))
	return hex.EncodeToString(hash[:])
}

func easyPayVerifySign(params map[string]string, pkey string, sign string) bool {
	return hmac.Equal([]byte(easyPaySign(params, pkey)), []byte(sign))
}

// easyPayNotifyRequiredParams are the fields a genuine EasyPay-compatible async
// notification must carry, and the only ones the business layer reads:
//
//   - pid           binds the callback to the merchant instance,
//   - out_trade_no  resolves the local order (routing happens on this field),
//   - trade_no      is the gateway transaction used by the settlement query,
//   - money         is compared against payment_orders.pay_amount,
//   - trade_status  gates success (exact TRADE_SUCCESS only),
//   - sign          authenticates all of the above.
//
// Unknown provider-generated fields are accepted as long as the signature over
// the full non-empty parameter set matches; see VerifyNotification.
var easyPayNotifyRequiredParams = []string{
	"pid",
	"out_trade_no",
	"trade_no",
	"money",
	"trade_status",
	"sign",
}

// easyPayNotifyRejectedCarrierParams are request-side fields of the signed
// checkout URL that a genuine async notification never repeats. They exist as a
// narrow #7881 guard, not as a callback allowlist: a replay needs one of them
// (their value is where an embedded query string can hide), while every
// provider-generated field — known or not — is still accepted when signed.
var easyPayNotifyRejectedCarrierParams = map[string]bool{
	"return_url": true,
	"notify_url": true,
}

// easyPayNotifyParamPairNames are parameter names that can steer EasyPay
// notifications or the checkout request. easyPayNotifyValueCarriesParamPair only
// uses them to spot an embedded serialized "&name=value" pair; this is NOT a
// callback allowlist — any other provider-generated field is still accepted when
// the signature covers it.
var easyPayNotifyParamPairNames = map[string]bool{
	"pid":            true,
	"trade_no":       true,
	"out_trade_no":   true,
	"type":           true,
	"name":           true,
	"money":          true,
	"trade_status":   true,
	"param":          true,
	"sign":           true,
	"sign_type":      true,
	"return_url":     true,
	"notify_url":     true,
	"cid":            true,
	"device":         true,
	"clientip":       true,
	"method":         true,
	"auth_code":      true,
	"sub_openid":     true,
	"sub_appid":      true,
	"timestamp":      true,
	"bill_trade_no":  true,
	"api_trade_no":   true,
	"buyer":          true,
	"subject":        true,
	"trade_time":     true,
	"addtime":        true,
	"endtime":        true,
	"custom_data":    true,
}

// easyPayNotifyValueCarriesParamPair reports whether a decoded callback value
// contains an embedded serialized parameter pair such as
// "&trade_status=TRADE_SUCCESS".
//
// This guards the upstream #7881 replay: easyPaySign concatenates values with
// '&' but does not escape them, so a signed checkout URL that embeds a query
// string inside a parameter value (historically return_url) can be re-cut into
// extra top-level parameters while the MD5 base string — and therefore the
// signature — stays byte-identical. Re-cutting either leaves a carrier key at
// the top level (rejected by easyPayNotifyRejectedCarrierParams) or pushes its
// text inside another value as "&key=value" (rejected here), so the smuggling
// class is closed without name-allowlisting provider fields. Genuine callback
// values (order/product names, numeric transaction ids, timestamps) do not
// contain such a pair.
func easyPayNotifyValueCarriesParamPair(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '&' {
			continue
		}
		rest := value[i+1:]
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			continue
		}
		name := strings.ToLower(rest[:eq])
		if easyPayNotifyParamPairNames[name] {
			return true
		}
	}
	return false
}
