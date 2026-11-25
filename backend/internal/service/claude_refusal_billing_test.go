//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Claude 违规拒答（stop_reason=refusal）照常计费：余额、订阅、key 额度、账号额度、账号统计成本与「同 token 数的普通请求」
// 逐项一致，唯一区别是用量行 request_type=claude_refusal（stream 标志不丢），便于按用户、按类型查询。

const claudeRefusalBillingModel = "claude-sonnet-4"

type claudeRefusalBillingFixture struct {
	usageRepo   *openAIRecordUsageLogRepoStub
	userRepo    *openAIRecordUsageUserRepoStub
	subRepo     *openAIRecordUsageSubRepoStub
	quotaSvc    *openAIRecordUsageAPIKeyQuotaStub
	billingRepo *openAIRecordUsageBillingRepoStub
	svc         *GatewayService
	input       *RecordUsageInput
}

// newClaudeRefusalBillingFixture 搭一条「真有钱可扣」的链路：余额/订阅/key 额度/账号额度/账号统计规则全部打开，
// 这样正常请求每一项都会 > 0，拒答只要漏掉任何一项这里都会失败。
func newClaudeRefusalBillingFixture(t *testing.T, usage ClaudeUsage, stream, subscription, useBillingRepo bool) *claudeRefusalBillingFixture {
	t.Helper()
	f := &claudeRefusalBillingFixture{
		usageRepo:   &openAIRecordUsageLogRepoStub{inserted: true},
		userRepo:    &openAIRecordUsageUserRepoStub{},
		subRepo:     &openAIRecordUsageSubRepoStub{},
		quotaSvc:    &openAIRecordUsageAPIKeyQuotaStub{},
		billingRepo: &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}},
	}
	if useBillingRepo {
		f.svc = newGatewayRecordUsageServiceWithBillingRepoForTest(f.usageRepo, f.billingRepo, f.userRepo, f.subRepo)
	} else {
		f.svc = newGatewayRecordUsageServiceForTest(f.usageRepo, f.userRepo, f.subRepo)
	}
	groupID := int64(7001)
	f.svc.channelService = newTestChannelServiceForStats(t, &Channel{
		ID: 1, Status: StatusActive,
		AccountStatsPricingRules: []AccountStatsPricingRule{{
			GroupIDs: []int64{groupID},
			Pricing: []ChannelModelPricing{{
				Models: []string{claudeRefusalBillingModel}, BillingMode: BillingModeToken,
				InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
			}},
		}},
	}, groupID, PlatformAnthropic)
	f.svc.resolver = NewModelPricingResolver(f.svc.channelService, f.svc.billingService)

	group := &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}
	var sub *UserSubscription
	if subscription {
		group.SubscriptionType = SubscriptionTypeSubscription
		sub = &UserSubscription{ID: 9}
	}
	account := &Account{ID: 701, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	if useBillingRepo {
		// 账号额度只在统一结算（billing repo）里扣；legacy 兜底路径没有 accountRepo，不开。
		account.Extra = map[string]any{"quota_limit": 100.0}
	}
	f.input = &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "req_claude_refusal_billing",
			Usage:     usage,
			Model:     claudeRefusalBillingModel,
			Stream:    stream,
			Duration:  time.Second,
		},
		APIKey:        &APIKey{ID: 501, Quota: 100, GroupID: &groupID, Group: group},
		User:          &User{ID: 601},
		Account:       account,
		Subscription:  sub,
		APIKeyService: f.quotaSvc,
	}
	return f
}

func (f *claudeRefusalBillingFixture) record(t *testing.T) *UsageLog {
	t.Helper()
	require.NoError(t, f.svc.RecordUsage(context.Background(), f.input))
	require.Equal(t, 1, f.usageRepo.calls)
	require.NotNil(t, f.usageRepo.lastLog)
	return f.usageRepo.lastLog
}

func TestGatewayServiceRecordUsage_ClaudeRefusalIsBilledAndTagged(t *testing.T) {
	refusal := ClaudeUsage{InputTokens: 44, Refusal: true}
	sameTokens := ClaudeUsage{InputTokens: 44} // 对照组：同样的 token 数，没有拒答标志
	normal := ClaudeUsage{InputTokens: 44, OutputTokens: 12}

	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("balance_legacy_path/stream=%t", stream), func(t *testing.T) {
			f := newClaudeRefusalBillingFixture(t, refusal, stream, false, false)
			log := f.record(t)
			ctl := newClaudeRefusalBillingFixture(t, sameTokens, stream, false, false)
			ctlLog := ctl.record(t)

			require.Positive(t, log.TotalCost, "上游照常收费，拒答也照常计费")
			require.Positive(t, log.ActualCost)
			require.InDelta(t, ctlLog.TotalCost, log.TotalCost, 1e-12, "与同 token 数的普通请求金额一致")
			require.InDelta(t, ctlLog.ActualCost, log.ActualCost, 1e-12)
			require.Equal(t, 44, log.InputTokens)
			require.Zero(t, log.OutputTokens)
			require.Equal(t, RequestTypeClaudeRefusal, log.RequestType)
			require.NotEqual(t, RequestTypeClaudeRefusal, ctlLog.RequestType, "只有拒答打标记")
			require.Equal(t, stream, log.Stream)
			require.NotNil(t, log.BillingMode)
			require.Equal(t, string(BillingModeToken), *log.BillingMode)
			require.NotNil(t, log.AccountStatsCost, "账号统计成本照常算")
			require.NotNil(t, ctlLog.AccountStatsCost)
			require.Positive(t, *log.AccountStatsCost)
			require.InDelta(t, *ctlLog.AccountStatsCost, *log.AccountStatsCost, 1e-12)
			require.Equal(t, 1, f.userRepo.deductCalls, "余额照常扣")
			require.InDelta(t, log.ActualCost, f.userRepo.lastAmount, 1e-12)
			require.Equal(t, 1, f.quotaSvc.quotaCalls, "key 额度照常扣")
			require.Equal(t, ctl.quotaSvc.rateLimitCalls, f.quotaSvc.rateLimitCalls, "key 限速额度与普通请求一致")

			// 入库前的 legacy 字段同步：request_type 不变，stream 标志不丢，也不会被当成 ws。
			log.SyncRequestTypeAndLegacyFields()
			require.Equal(t, RequestTypeClaudeRefusal, log.RequestType)
			require.Equal(t, stream, log.Stream)
			require.False(t, log.OpenAIWSMode)
		})

		t.Run(fmt.Sprintf("balance_billing_repo/stream=%t", stream), func(t *testing.T) {
			f := newClaudeRefusalBillingFixture(t, refusal, stream, false, true)
			log := f.record(t)
			ctl := newClaudeRefusalBillingFixture(t, sameTokens, stream, false, true)
			ctl.record(t)

			require.Equal(t, RequestTypeClaudeRefusal, log.RequestType)
			require.Equal(t, 1, f.billingRepo.calls)
			cmd, ctlCmd := f.billingRepo.lastCmd, ctl.billingRepo.lastCmd
			require.NotNil(t, cmd)
			require.NotNil(t, ctlCmd)
			require.Positive(t, cmd.BalanceCost)
			require.Positive(t, cmd.APIKeyQuotaCost)
			require.Positive(t, cmd.AccountQuotaCost, "账号额度照常扣")
			require.InDelta(t, ctlCmd.BalanceCost, cmd.BalanceCost, 1e-12)
			require.InDelta(t, ctlCmd.SubscriptionCost, cmd.SubscriptionCost, 1e-12)
			require.InDelta(t, ctlCmd.APIKeyQuotaCost, cmd.APIKeyQuotaCost, 1e-12)
			require.InDelta(t, ctlCmd.APIKeyRateLimitCost, cmd.APIKeyRateLimitCost, 1e-12)
			require.InDelta(t, ctlCmd.AccountQuotaCost, cmd.AccountQuotaCost, 1e-12)
			require.Equal(t, 44, cmd.InputTokens)
		})
	}

	t.Run("subscription_legacy_path", func(t *testing.T) {
		f := newClaudeRefusalBillingFixture(t, refusal, true, true, false)
		log := f.record(t)
		ctl := newClaudeRefusalBillingFixture(t, sameTokens, true, true, false)
		ctlLog := ctl.record(t)
		require.Equal(t, BillingTypeSubscription, log.BillingType)
		require.Equal(t, RequestTypeClaudeRefusal, log.RequestType)
		require.Positive(t, log.ActualCost)
		require.InDelta(t, ctlLog.ActualCost, log.ActualCost, 1e-12)
		require.Equal(t, 1, f.subRepo.incrementCalls, "订阅额度照常扣")
		require.Equal(t, ctl.subRepo.incrementCalls, f.subRepo.incrementCalls)
		require.Zero(t, f.userRepo.deductCalls, "订阅计费不动余额")
	})

	t.Run("subscription_billing_repo", func(t *testing.T) {
		f := newClaudeRefusalBillingFixture(t, refusal, true, true, true)
		f.record(t)
		ctl := newClaudeRefusalBillingFixture(t, sameTokens, true, true, true)
		ctl.record(t)
		require.NotNil(t, f.billingRepo.lastCmd)
		require.Positive(t, f.billingRepo.lastCmd.SubscriptionCost)
		require.InDelta(t, ctl.billingRepo.lastCmd.SubscriptionCost, f.billingRepo.lastCmd.SubscriptionCost, 1e-12)
		require.Zero(t, f.billingRepo.lastCmd.BalanceCost)
	})

	// 对照组：同样的链路，没有 refusal 标志的正常请求照常计费，所有金额项都 > 0。
	t.Run("control_normal_request_is_billed", func(t *testing.T) {
		f := newClaudeRefusalBillingFixture(t, normal, true, false, false)
		log := f.record(t)
		require.Positive(t, log.TotalCost)
		require.Positive(t, log.ActualCost)
		require.Equal(t, RequestTypeUnknown, log.RequestType, "正常请求不打拒答标记，类型仍由 stream 推导")
		require.True(t, log.Stream)
		require.NotNil(t, log.AccountStatsCost)
		require.Positive(t, *log.AccountStatsCost)
		require.Equal(t, 1, f.userRepo.deductCalls)
		require.InDelta(t, log.ActualCost, f.userRepo.lastAmount, 1e-12)
		require.Equal(t, 1, f.quotaSvc.quotaCalls)
	})

	t.Run("control_normal_request_billing_repo", func(t *testing.T) {
		f := newClaudeRefusalBillingFixture(t, normal, false, false, true)
		log := f.record(t)
		require.NotNil(t, f.billingRepo.lastCmd)
		require.Positive(t, f.billingRepo.lastCmd.BalanceCost)
		require.Positive(t, f.billingRepo.lastCmd.APIKeyQuotaCost)
		require.Positive(t, f.billingRepo.lastCmd.AccountQuotaCost)
		require.InDelta(t, log.ActualCost, f.billingRepo.lastCmd.BalanceCost, 1e-9)
	})

	// 对照组：上游真错误（NonBillableUpstreamError）仍走原有分支，且不会被打成拒答类型。
	t.Run("control_upstream_error_keeps_existing_behavior", func(t *testing.T) {
		f := newClaudeRefusalBillingFixture(t, normal, true, false, false)
		f.input.Result.NonBillableUpstreamError = true
		log := f.record(t)
		require.Zero(t, log.ActualCost)
		require.Equal(t, RequestTypeUnknown, log.RequestType)
	})
}

// 端到端：上游原始响应 → Forward → ForwardResult → RecordUsage，覆盖通用路径和透传路径的流式/非流式。
func TestClaudeRefusalForwardEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const model = claudeRefusalBillingModel

	type pathCase struct {
		name    string
		stream  bool
		account func() *Account
	}
	genericAPIKey := func() *Account {
		a := newAnthropicAPIKeyAccountForTest()
		a.Extra["anthropic_passthrough"] = false
		return a
	}
	paths := []pathCase{
		{"oauth_generic_stream", true, newAnthropicOAuthAccountForPartialUsageTest},
		{"apikey_generic_stream", true, genericAPIKey},
		{"apikey_passthrough_stream", true, newAnthropicAPIKeyAccountForTest},
		{"apikey_generic_non_stream", false, genericAPIKey},
		{"apikey_passthrough_non_stream", false, newAnthropicAPIKeyAccountForTest},
	}

	run := func(t *testing.T, p pathCase, upstreamBody string) (*ForwardResult, string) {
		t.Helper()
		contentType := "application/json"
		if p.stream {
			contentType = "text/event-stream"
		}
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {contentType}},
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		}}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		reqBody := `{"model":"` + model + `","messages":[{"role":"user","content":"synthetic check"}]}`
		if p.stream {
			reqBody = `{"model":"` + model + `","stream":true,"messages":[{"role":"user","content":"synthetic check"}]}`
		}
		result, err := newForwardPartialUsageServiceForTest(upstream).Forward(context.Background(), c, p.account(), &ParsedRequest{
			Body: NewRequestBodyRef([]byte(reqBody)), Model: model, Stream: p.stream,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		return result, rec.Body.String()
	}

	settle := func(t *testing.T, result *ForwardResult) (*UsageLog, *openAIRecordUsageBillingRepoStub) {
		t.Helper()
		usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
		billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
		svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
		require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
			Result:  result,
			APIKey:  &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
			User:    &User{ID: 2000},
			Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
		}))
		require.Equal(t, 1, usageRepo.calls)
		require.Equal(t, 1, billingRepo.calls)
		require.NotNil(t, billingRepo.lastCmd)
		return usageRepo.lastLog, billingRepo
	}

	for _, p := range paths {
		p := p
		refusalBody := claudeRefusalJSONBody
		normalBody := claudeControlJSONBody(`"end_turn"`, "hello")
		if p.stream {
			refusalBody = claudeRefusalStreamSSE
			normalBody = claudeControlStreamSSE(`"end_turn"`, "hello", true)
		}

		t.Run(p.name+"/refusal_is_billed_and_tagged", func(t *testing.T) {
			result, clientBody := run(t, p, refusalBody)
			require.True(t, result.Usage.Refusal)
			require.False(t, result.NonBillableUpstreamError, "拒答不是上游错误，走独立标志")
			require.Equal(t, p.stream, result.Stream)
			require.Equal(t, 44, result.Usage.InputTokens)
			require.Contains(t, clientBody, `"stop_reason":"refusal"`, "客户端响应保持上游原样")

			log, billingRepo := settle(t, result)
			require.Equal(t, RequestTypeClaudeRefusal, log.RequestType)
			require.Equal(t, p.stream, log.Stream)
			require.Equal(t, 44, log.InputTokens)
			require.Positive(t, log.TotalCost, "上游照常收费，拒答也照常计费")
			require.Positive(t, log.ActualCost)
			require.Positive(t, billingRepo.lastCmd.BalanceCost)
		})

		t.Run(p.name+"/normal_answer_still_billed", func(t *testing.T) {
			result, _ := run(t, p, normalBody)
			require.False(t, result.Usage.Refusal)
			log, billingRepo := settle(t, result)
			require.Equal(t, RequestTypeUnknown, log.RequestType)
			require.Positive(t, log.ActualCost)
			require.Positive(t, billingRepo.lastCmd.BalanceCost)
		})
	}
}
