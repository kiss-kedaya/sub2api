package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
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

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

const (
	epusdtCreatePath      = "/payments/gmpay/v1/order/create-transaction"
	epusdtStatusPath      = "/pay/check-status/"
	epusdtHTTPTimeout     = 12 * time.Second
	epusdtMaxResponseSize = 1 << 20
)

type EPUSDT struct {
	instanceID string
	config     map[string]string
	httpClient *http.Client
}

type epusdtEnvelope struct {
	StatusCode int             `json:"status_code"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
}

type epusdtCreateData struct {
	TradeID    string  `json:"trade_id"`
	OrderID    string  `json:"order_id"`
	Amount     float64 `json:"amount"`
	PaymentURL string  `json:"payment_url"`
}

type epusdtStatusData struct {
	TradeID string `json:"trade_id"`
	Status  int    `json:"status"`
}

func NewEPUSDT(instanceID string, config map[string]string) (*EPUSDT, error) {
	cfg := cloneStringMap(config)
	for _, key := range []string{"pid", "secretKey", "apiBase", "notifyUrl", "returnUrl", "token", "network", "currency"} {
		if strings.TrimSpace(cfg[key]) == "" {
			return nil, fmt.Errorf("epusdt config missing required key: %s", key)
		}
	}

	base, err := normalizeEPUSDTURL(cfg["apiBase"], "apiBase")
	if err != nil {
		return nil, err
	}
	notify, err := normalizeEPUSDTURL(cfg["notifyUrl"], "notifyUrl")
	if err != nil {
		return nil, err
	}
	returnURL, err := normalizeEPUSDTURL(cfg["returnUrl"], "returnUrl")
	if err != nil {
		return nil, err
	}
	currency, err := payment.NormalizePaymentCurrency(cfg["currency"])
	if err != nil {
		return nil, fmt.Errorf("epusdt config currency: %w", err)
	}

	pid := strings.TrimSpace(cfg["pid"])
	if len(pid) > 64 {
		return nil, fmt.Errorf("epusdt pid is too long")
	}
	token := strings.ToUpper(strings.TrimSpace(cfg["token"]))
	network := strings.ToLower(strings.TrimSpace(cfg["network"]))
	if token == "" || network == "" {
		return nil, fmt.Errorf("epusdt token and network are required")
	}

	cfg["apiBase"] = base
	cfg["notifyUrl"] = notify
	cfg["returnUrl"] = returnURL
	cfg["pid"] = pid
	cfg["token"] = token
	cfg["network"] = network
	cfg["currency"] = currency

	return &EPUSDT{
		instanceID: instanceID,
		config:     cfg,
		httpClient: &http.Client{Timeout: epusdtHTTPTimeout},
	}, nil
}

func normalizeEPUSDTURL(raw, field string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("epusdt %s must be an HTTPS URL without query or fragment", field)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// normalizeEPUSDTOrderReturnURL validates the per-order redirect URL after the
// payment service has appended its signed/order-resume query parameters. The
// configured returnUrl remains strict; only HTTPS URLs on that same host are
// accepted here.
func normalizeEPUSDTOrderReturnURL(raw, configured string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", fmt.Errorf("epusdt returnUrl must be an HTTP or HTTPS URL")
	}
	base, err := url.Parse(strings.TrimSpace(configured))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return "", fmt.Errorf("epusdt returnUrl configuration is invalid")
	}
	if strings.EqualFold(parsed.Scheme, "http") {
		// The site can be opened through the legacy HTTP/IP entrypoint. EPUSDT
		// requires HTTPS, so canonicalize that accepted same-site origin to the
		// configured HTTPS merchant origin instead of forwarding an HTTP URL.
		parsed.Scheme = base.Scheme
		parsed.Host = base.Host
		parsed.User = nil
	} else if !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
		return "", fmt.Errorf("epusdt returnUrl host mismatch")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "?"), nil
}

func (e *EPUSDT) Name() string { return "EPUSDT" }

func (e *EPUSDT) ProviderKey() string { return payment.TypeEpusdt }

func (e *EPUSDT) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeEpusdt}
}

func (e *EPUSDT) MerchantIdentityMetadata() map[string]string {
	return map[string]string{
		"pid":      e.config["pid"],
		"token":    e.config["token"],
		"network":  e.config["network"],
		"currency": e.config["currency"],
	}
}

func (e *EPUSDT) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	amount, err := strconv.ParseFloat(strings.TrimSpace(req.Amount), 64)
	if err != nil || !isFinitePositive(amount) {
		return nil, fmt.Errorf("epusdt create payment: invalid amount")
	}
	orderID := strings.TrimSpace(req.OrderID)
	if orderID == "" || len([]byte(orderID)) > 32 {
		return nil, fmt.Errorf("epusdt create payment: order id must be 1-32 bytes")
	}

	notifyURL := strings.TrimSpace(req.NotifyURL)
	if notifyURL == "" {
		notifyURL = e.config["notifyUrl"]
	}
	returnURL := strings.TrimSpace(req.ReturnURL)
	if returnURL == "" {
		returnURL = e.config["returnUrl"]
	}
	notifyURL, err = normalizeEPUSDTURL(notifyURL, "notifyUrl")
	if err != nil {
		return nil, err
	}
	// The payment service appends order_id/out_trade_no/status (and, for
	// resumable flows, a resume token) to the merchant return URL before
	// invoking the provider. Keep the configured returnUrl strict, but allow
	// this controlled query string on the per-order redirect URL.
	returnURL, err = normalizeEPUSDTOrderReturnURL(returnURL, e.config["returnUrl"])
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"pid":          e.config["pid"],
		"order_id":     orderID,
		"currency":     e.config["currency"],
		"token":        e.config["token"],
		"network":      e.config["network"],
		"amount":       amount,
		"notify_url":   notifyURL,
		"redirect_url": returnURL,
		"name":         strings.TrimSpace(req.Subject),
	}
	payload["signature"] = epusdtSign(payload, e.config["secretKey"])

	var data epusdtCreateData
	if err := e.doJSON(ctx, http.MethodPost, epusdtCreatePath, payload, &data); err != nil {
		return nil, fmt.Errorf("epusdt create payment: %w", err)
	}
	data.TradeID = strings.TrimSpace(data.TradeID)
	data.OrderID = strings.TrimSpace(data.OrderID)
	if data.TradeID == "" || strings.TrimSpace(data.PaymentURL) == "" {
		return nil, fmt.Errorf("epusdt create payment: incomplete response")
	}
	if data.OrderID != "" && data.OrderID != orderID {
		return nil, fmt.Errorf("epusdt create payment: order id mismatch")
	}
	payURL, err := e.normalizePaymentURL(data.PaymentURL)
	if err != nil {
		return nil, err
	}

	return &payment.CreatePaymentResponse{
		TradeNo:    data.TradeID,
		PayURL:     payURL,
		Currency:   e.config["currency"],
		ResultType: payment.CreatePaymentResultOrderCreated,
	}, nil
}

func (e *EPUSDT) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	tradeNo = strings.TrimSpace(tradeNo)
	if tradeNo == "" {
		return nil, fmt.Errorf("epusdt query order: missing trade number")
	}

	var data epusdtStatusData
	if err := e.doJSON(ctx, http.MethodGet, epusdtStatusPath+url.PathEscape(tradeNo), nil, &data); err != nil {
		return nil, fmt.Errorf("epusdt query order: %w", err)
	}

	status := payment.ProviderStatusPending
	switch data.Status {
	case 1, 4:
		status = payment.ProviderStatusPending
	case 2:
		status = payment.ProviderStatusPaid
	case 3:
		status = payment.ProviderStatusFailed
	default:
		return nil, fmt.Errorf("epusdt query order: unsupported status %d", data.Status)
	}
	data.TradeID = strings.TrimSpace(data.TradeID)
	if data.TradeID == "" {
		data.TradeID = tradeNo
	}
	return &payment.QueryOrderResponse{TradeNo: data.TradeID, Status: status}, nil
}

func (e *EPUSDT) VerifyNotification(_ context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(rawBody))
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return nil, fmt.Errorf("epusdt webhook: invalid JSON")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("epusdt webhook: invalid JSON")
	}

	signature := rawString(raw, "signature")
	if len(signature) != sha256.Size*2 {
		return nil, fmt.Errorf("epusdt webhook: missing or invalid signature")
	}
	if _, err := hex.DecodeString(signature); err != nil {
		return nil, fmt.Errorf("epusdt webhook: invalid signature")
	}
	if rawString(raw, "pid") != e.config["pid"] {
		return nil, fmt.Errorf("epusdt webhook: pid mismatch")
	}

	values := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		if key == "signature" {
			continue
		}
		var decoded interface{}
		fieldDecoder := json.NewDecoder(bytes.NewReader(value))
		fieldDecoder.UseNumber()
		if err := fieldDecoder.Decode(&decoded); err != nil {
			return nil, fmt.Errorf("epusdt webhook: invalid field")
		}
		values[key] = decoded
	}
	expected := epusdtSign(values, e.config["secretKey"])
	if !hmac.Equal([]byte(strings.ToLower(signature)), []byte(expected)) {
		return nil, fmt.Errorf("epusdt webhook: signature mismatch")
	}

	orderID := rawString(raw, "order_id")
	tradeID := rawString(raw, "trade_id")
	if orderID == "" || tradeID == "" {
		return nil, fmt.Errorf("epusdt webhook: missing order or trade id")
	}
	amount, err := rawFloat(raw, "amount")
	if err != nil || !isFinitePositive(amount) {
		return nil, fmt.Errorf("epusdt webhook: invalid amount")
	}
	upstreamStatus := rawInt(raw, "status")
	status := payment.ProviderStatusFailed
	if upstreamStatus == 2 {
		status = payment.NotificationStatusSuccess
	}

	// Keep webhook audit data minimal: signatures and wallet addresses are not persisted.
	return &payment.PaymentNotification{
		TradeNo: tradeID,
		OrderID: orderID,
		Amount:  amount,
		Status:  status,
		RawData: fmt.Sprintf(`{"order_id":%q,"trade_id":%q,"status":%d}`, orderID, tradeID, upstreamStatus),
		Metadata: map[string]string{
			"pid":                  e.config["pid"],
			"token":                rawString(raw, "token"),
			"block_transaction_id": rawString(raw, "block_transaction_id"),
		},
	}, nil
}

func (e *EPUSDT) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("epusdt refunds are not supported")
}

func (e *EPUSDT) normalizePaymentURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" && parsed.Host != "" {
		return "", fmt.Errorf("epusdt payment URL is invalid")
	}
	base, err := url.Parse(e.config["apiBase"])
	if err != nil {
		return "", fmt.Errorf("epusdt payment URL is invalid")
	}
	if !parsed.IsAbs() {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("epusdt payment URL must be HTTPS")
	}
	if !strings.EqualFold(parsed.Host, base.Host) {
		return "", fmt.Errorf("epusdt payment URL host mismatch")
	}
	return parsed.String(), nil
}

func (e *EPUSDT) doJSON(ctx context.Context, method, path string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.config["apiBase"]+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, epusdtMaxResponseSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(data) > epusdtMaxResponseSize {
		return fmt.Errorf("response too large")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}

	var envelope epusdtEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("invalid response")
	}
	if envelope.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream status %d: %s", envelope.StatusCode, strings.TrimSpace(envelope.Message))
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return fmt.Errorf("missing response data")
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("invalid response data")
	}
	return nil
}

func epusdtSign(values map[string]interface{}, secret string) string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if key == "signature" || value == nil {
			continue
		}
		formatted, ok := epusdtValue(value)
		if !ok || formatted == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		formatted, _ := epusdtValue(values[key])
		parts = append(parts, key+"="+formatted)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}

func epusdtValue(value interface{}) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || !isFinite(parsed) {
			return "", false
		}
		return strconv.FormatFloat(parsed, 'f', -1, 64), true
	case float64:
		if !isFinite(typed) {
			return "", false
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isFinitePositive(value float64) bool {
	return isFinite(value) && value > 0
}

func rawString(raw map[string]json.RawMessage, key string) string {
	value, ok := raw[key]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return strings.TrimSpace(string(number))
	}
	return ""
}

func rawFloat(raw map[string]json.RawMessage, key string) (float64, error) {
	value, ok := raw[key]
	if !ok {
		return 0, fmt.Errorf("missing field")
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(string(number), 64)
}

func rawInt(raw map[string]json.RawMessage, key string) int {
	value, ok := raw[key]
	if !ok {
		return 0
	}
	var result int
	_ = json.Unmarshal(value, &result)
	return result
}
