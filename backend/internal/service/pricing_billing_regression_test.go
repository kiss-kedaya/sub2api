//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIGatewayServiceRecordUsage_NonBillableFreeFastDoesNotCharge(t *testing.T) {
	for _, tt := range []struct {
		name             string
		subscriptionType string
		subscription     *UserSubscription
	}{
		{name: "balance"},
		{name: "subscription", subscriptionType: SubscriptionTypeSubscription, subscription: &UserSubscription{ID: 400}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(77)
			serviceTier := "priority"
			inputPrice := 0.001
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID: "rejected-free-fast-" + tt.name, ServiceTier: &serviceTier,
					Usage: OpenAIUsage{InputTokens: 1000}, Model: "gpt-5.6-sol", Duration: time.Second,
					NonBillableUpstreamError: true,
				},
				APIKey: &APIKey{ID: 100, GroupID: &groupID, Group: &Group{
					ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
					RateMultiplier: 1, FreeOpenAIFast: true, SubscriptionType: tt.subscriptionType,
					ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-5.6-sol"}, BillingMode: BillingModeToken, InputPrice: &inputPrice}},
				}},
				User: &User{ID: 200}, Account: &Account{ID: 300, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				Subscription: tt.subscription,
			})
			require.NoError(t, err)
			require.NotNil(t, billingRepo.lastCmd)
			require.Zero(t, billingRepo.lastCmd.BalanceCost, "rejected request must not deduct balance")
			require.Zero(t, billingRepo.lastCmd.SubscriptionCost, "rejected request must not consume subscription quota")
			require.Zero(t, usageRepo.lastLog.TotalCost)
			require.Zero(t, usageRepo.lastLog.ActualCost)
		})
	}
}

func TestGroupPricing_AcceptedModelVariantResolution(t *testing.T) {
	price := func(models []string, perMillion float64) ChannelModelPricing {
		inputPrice := perMillion / 1e6
		return ChannelModelPricing{Models: models, BillingMode: BillingModeToken, InputPrice: &inputPrice}
	}
	basePricing := price([]string{"gpt-5.6-luna"}, 0.4)

	for _, tt := range []struct {
		name           string
		requestedModel string
		mappedModel    string
		pricing        []ChannelModelPricing
		wantForwarded  string
		wantCost       float64
	}{
		{name: "base_name", requestedModel: "gpt-5.6-luna", pricing: []ChannelModelPricing{basePricing}, wantForwarded: "gpt-5.6-luna", wantCost: 0.4},
		{name: "known_effort_suffix_uses_base", requestedModel: "gpt-5.6-luna-high", pricing: []ChannelModelPricing{basePricing}, wantForwarded: "gpt-5.6-luna-high", wantCost: 0.4},
		{name: "known_date_suffix_uses_base", requestedModel: "gpt-5.6-luna-2026-08-01", pricing: []ChannelModelPricing{basePricing}, wantForwarded: "gpt-5.6-luna-2026-08-01", wantCost: 0.4},
		{name: "exact_variant_wins", requestedModel: "gpt-5.6-luna-high", pricing: []ChannelModelPricing{basePricing, price([]string{"gpt-5.6-luna-high"}, 0.9)}, wantForwarded: "gpt-5.6-luna-high", wantCost: 0.9},
		{name: "literal_wildcard_wins", requestedModel: "gpt-5.6-luna-high", pricing: []ChannelModelPricing{basePricing, price([]string{"gpt-5.6-luna-*"}, 0.7)}, wantForwarded: "gpt-5.6-luna-high", wantCost: 0.7},
		{name: "account_mapping_to_base", requestedModel: "public-luna-high", mappedModel: "gpt-5.6-luna", pricing: []ChannelModelPricing{basePricing}, wantForwarded: "gpt-5.6-luna", wantCost: 0.4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			account := newOpenAIRejectedFieldTestAccount()
			if tt.mappedModel != "" {
				account.Credentials["model_mapping"] = map[string]any{tt.requestedModel: tt.mappedModel}
			}
			require.True(t, account.IsModelSupported(tt.requestedModel))
			body, err := json.Marshal(map[string]any{"model": tt.requestedModel, "input": "hello", "stream": false})
			require.NoError(t, err)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				newOpenAIRejectedFieldTestResponse(http.StatusOK,
					`{"id":"group-price-regression","output":[],"usage":{"input_tokens":1000000,"output_tokens":0}}`),
			}}
			result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
				context.Background(), newOpenAIRejectedFieldTestContext(body), account, body,
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.bodies, 1)
			forwardedModel := gjson.GetBytes(upstream.bodies[0], "model").String()
			require.Equal(t, tt.wantForwarded, forwardedModel)
			require.Equal(t, tt.wantForwarded, result.BillingModel)
			require.Equal(t, 1000000, result.Usage.InputTokens)
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(78)
			err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: result,
				ChannelUsageFields: ChannelUsageFields{
					OriginalModel: tt.requestedModel, ChannelMappedModel: forwardedModel,
					BillingModelSource: BillingModelSourceChannelMapped,
				},
				APIKey: &APIKey{ID: 101, GroupID: &groupID, Group: &Group{
					ID: groupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 1,
					ModelPricing: tt.pricing,
				}},
				User: &User{ID: 201}, Account: account,
			})
			require.NoError(t, err)
			require.NotNil(t, billingRepo.lastCmd)
			require.InDelta(t, tt.wantCost, billingRepo.lastCmd.BalanceCost, 1e-9)
		})
	}
}
