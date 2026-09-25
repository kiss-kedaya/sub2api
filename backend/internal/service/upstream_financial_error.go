package service

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const UpstreamUnavailableMessage = "Upstream service temporarily unavailable"
const upstreamFinancialErrorSentKey = "upstream_financial_error_sent"
const upstreamFinancialErrorGuardKey = "upstream_financial_error_guard"

// Only call at an upstream failure boundary. Local billing/authentication errors
// must never enter this classifier. Success content and usage counters are not searched.
func IsUpstreamFinancialError(status int, body []byte) bool {
	if status == http.StatusPaymentRequired {
		return true
	}
	if !gjson.ValidBytes(body) {
		return upstreamFinancialMessage(string(body))
	}
	return upstreamFinancialNode(gjson.ParseBytes(body), 0)
}

func upstreamFinancialNode(node gjson.Result, depth int) bool {
	if depth > 12 {
		return false
	}
	if node.Type == gjson.String {
		text := strings.TrimSpace(node.String())
		if strings.HasPrefix(text, "{") && gjson.Valid(text) {
			return upstreamFinancialNode(gjson.Parse(text), depth+1)
		}
		return upstreamFinancialMessage(text)
	}
	if node.IsArray() {
		for _, value := range node.Array() {
			if upstreamFinancialNode(value, depth+1) {
				return true
			}
		}
		return false
	}
	if !node.IsObject() {
		return false
	}
	if node.Get("code").Int() == http.StatusPaymentRequired || node.Get("status_code").Int() == http.StatusPaymentRequired {
		return true
	}
	for _, name := range []string{"code", "type", "reason", "ref_code"} {
		if upstreamFinancialCode(node.Get(name).String()) {
			return true
		}
	}
	for _, name := range []string{"message", "detail", "details", "errors", "error", "response.error", "metadata.raw"} {
		value := node.Get(name)
		if value.Exists() && upstreamFinancialNode(value, depth+1) {
			return true
		}
	}
	return false
}

func upstreamFinancialCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "insufficient_quota", "insufficient_user_quota", "insufficient_balance",
		"pre_consume_token_quota_failed", "insufficient_funds", "insufficient_credits",
		"credit_balance_too_low", "balance_not_enough", "balance_insufficient",
		"billing", "billing_error", "billing_service_error", "billing_not_active", "billing_not_enabled",
		"billing_disabled", "billing_hard_limit_reached", "billing_limit_exceeded",
		"billing_account_disabled", "billing_account_suspended", "billing_account_frozen",
		"payment_required", "usage_limit_reached", "usage_limit_exceeded",
		"daily_limit_exceeded", "weekly_limit_exceeded", "monthly_limit_exceeded",
		"spend_limit_exceeded", "budget_exceeded", "quota_exhausted", "model_price_error",
		"400001", "400901":
		return true
	}
	return false
}

var upstreamFinancialMessagePattern = regexp.MustCompile(
	`(?i)(insufficient (?:account |user |token |wallet )?(?:balance|quota|funds|credits)|(?:balance|credit balance|credits?|wallet|subscription quota)[^\n.!?]{0,48}(?:insufficient|too low|exhausted|depleted|not enough)|(?:run |running )?out of credits|(?:exceeded|reached|exhausted)[^\n.!?]{0,32}(?:current quota|usage limit|spending limit|billing limit|budget)|(?:daily|weekly|monthly|account|api|token)? ?(?:usage|spending|billing|credit|budget) limit[^\n.!?]{0,40}(?:reach|exceed|exhaust)|(?:billing|payment|pre.?consum|pre.?authoriz|withholding)[^\n.!?]{0,48}(?:fail|disabled|not enabled|inactive|not active|frozen|suspended|unavailable|required|declined)|check your plan and billing|(?:余额|餘額|额度|額度|金额|费用|积分|点数|用量|消费|消費)[^\n。！？]{0,20}(?:不足|耗尽|耗盡|用尽|用完|超限|超出|已达|已達|限额|限額|扣除失败)|(?:预扣|預扣|扣费|扣費|计费|計費|账单|帳單)[^\n。！？]{0,24}(?:失败|失敗|冻结|凍結|异常|停用|未启用|不可用)|(?:剩余|剩餘|可用)(?:余额|餘額|额度|額度)|需要预扣费额度|credit balance is too low|included free usage|(?:balance|remaining quota|remaining credits)\s*[:=]\s*[$¥￥€£]?-?\d)`,
)

var upstreamFinancialAmountPattern = regexp.MustCompile(
	`(?i)(当前(?:额度|余额)|需要(?:额度|余额)|(?:预扣|扣费|计费|消费|收费|账单|費用)(?:明细|详情|金额)|(?:模型)?(?:价格|倍率)(?:未配置|未设置)|(?:balance|quota|credits?|cost|charge|price|billing|usage)(?:\s+(?:amount|remaining|used|cost|total))?\s*[:=]\s*[$¥￥€£]?-?\d|(?:charged|deducted|billed|precharged)\s+[$¥￥€£]?\d|[$¥￥€£]\s*\d[\d,.]*(?:\s*(?:balance|credits?|cost|charged|remaining)))`,
)

func upstreamFinancialMessage(message string) bool {
	if upstreamParameterErrorPattern.MatchString(message) {
		return false
	}
	return upstreamFinancialMessagePattern.MatchString(message) || upstreamFinancialAmountPattern.MatchString(message) || upstreamFinancialBalancePattern.MatchString(message)
}

var upstreamFinancialBalancePattern = regexp.MustCompile(`(?i)(exhausted[^\n.!?]{0,32}(?:credits|balance|funds)|(?:余额|餘額|额度|額度|金额|金額|费用|費用)\s*[:：=]\s*[$¥￥€£]?-?\d|(?:balance|cost|charge|credits)\s+(?:is|of)\s+[$¥￥€£]?\d)`)

var upstreamParameterErrorPattern = regexp.MustCompile(`(?i)^\s*(?:(?:invalid|unknown|unsupported|unrecognized|missing)\s+(?:value\s+for\s+)?(?:parameter|field|model|argument)\b|(?:参数|字段|模型)(?:错误|无效|不支持|不存在))`)

// GuardUpstreamFinancialError shields only writes made by this upstream error
// handler. It does not write on entry, change classification, or consume retries.
// Restoring the original writer leaves subsequent attempts and local billing alone.
func GuardUpstreamFinancialError(c *gin.Context, status int, body []byte) func() {
	if c == nil || c.Writer == nil || !IsUpstreamFinancialError(status, body) {
		return func() {}
	}
	if value, _ := c.Get(upstreamFinancialErrorGuardKey); value != nil {
		return func() {}
	}
	original := c.Writer
	guard := &upstreamFinancialErrorWriter{ResponseWriter: original, c: c}
	c.Set(upstreamFinancialErrorGuardKey, guard)
	c.Writer = guard
	return func() {
		c.Writer = original
		c.Set(upstreamFinancialErrorGuardKey, nil)
	}
}

type upstreamFinancialErrorWriter struct {
	gin.ResponseWriter
	c *gin.Context
}

func (w *upstreamFinancialErrorWriter) WriteHeader(_ int) {}
func (w *upstreamFinancialErrorWriter) WriteHeaderNow()   { _ = w.emit() }
func (w *upstreamFinancialErrorWriter) Write(p []byte) (int, error) {
	err := w.emit()
	return len(p), err
}
func (w *upstreamFinancialErrorWriter) WriteString(p string) (int, error) {
	err := w.emit()
	return len(p), err
}
func (w *upstreamFinancialErrorWriter) Flush() { _ = w.emit(); w.ResponseWriter.Flush() }
func (w *upstreamFinancialErrorWriter) emit() error {
	return writeUpstreamFinancialError(w.c, w.ResponseWriter)
}

// WriteUpstreamFinancialError is used only after existing stream classification
// and retry decisions. Returns true for a handled failure, including duplicates.
func WriteUpstreamFinancialError(c *gin.Context, status int, body []byte) bool {
	if c == nil || c.Writer == nil || !IsUpstreamFinancialError(status, body) {
		return false
	}
	w := c.Writer
	value, _ := c.Get(upstreamFinancialErrorGuardKey)
	if guard, ok := value.(*upstreamFinancialErrorWriter); ok {
		w = guard.ResponseWriter
	}
	_ = writeUpstreamFinancialError(c, w)
	return true
}

func writeUpstreamFinancialError(c *gin.Context, w gin.ResponseWriter) error {
	if c.GetBool(upstreamFinancialErrorSentKey) {
		return nil
	}
	// Stop heartbeat writers before checking commitment; they may flush concurrently.
	StopOpenAIImagesJSONKeepaliveCommitted(c)
	StopOpenAICompactSSEKeepaliveCommitted(c)
	committed := w.Written()
	c.Set(upstreamFinancialErrorSentKey, true)
	MarkResponseCommitted(c)
	path := c.FullPath()
	if c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	google := strings.Contains(path, ":generateContent") || strings.Contains(path, ":streamGenerateContent") || strings.Contains(path, ":countTokens")
	responses := strings.HasSuffix(strings.TrimRight(path, "/"), "/responses") || strings.Contains(path, "/responses/")
	images := strings.Contains(path, "/images/")
	anthropic := strings.HasSuffix(path, "/messages") || strings.HasSuffix(path, "/messages/count_tokens")
	errObject := gin.H{"type": "upstream_error", "message": UpstreamUnavailableMessage}
	envelope := gin.H{"error": errObject}
	if anthropic {
		envelope["type"] = "error"
	}
	if google {
		envelope["error"] = gin.H{"code": http.StatusBadGateway, "status": "UNAVAILABLE", "message": UpstreamUnavailableMessage}
	}
	jsonHeartbeat := OpenAIImagesJSONKeepalivePresent(c)
	if committed && responses && !jsonHeartbeat {
		payload := buildOpenAIResponseFailedSSE("", "", nil, UpstreamUnavailableMessage)
		_, err := w.WriteString(payload)
		w.Flush()
		return err
	}
	payload, _ := json.Marshal(envelope)
	if committed && jsonHeartbeat {
		_, err := w.Write(payload)
		return err
	}
	if committed {
		prefix := "data: "
		if anthropic || images {
			prefix = "event: error\ndata: "
		}
		_, err := w.WriteString(prefix + string(payload) + "\n\n")
		if !google && !anthropic && !images {
			_, _ = w.WriteString("data: [DONE]\n\n")
		}
		w.Flush()
		return err
	}
	// Discard upstream diagnostic, billing and credential headers, including any
	// custom response-header passthrough. Keep local CORS and security policy only.
	for key := range w.Header() {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "access-control-") || lower == "vary" || lower == "x-content-type-options" || lower == "content-security-policy" || lower == "strict-transport-security" || lower == "x-frame-options" {
			continue
		}
		w.Header().Del(key)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusBadGateway)
	_, err := w.Write(payload)
	return err
}
