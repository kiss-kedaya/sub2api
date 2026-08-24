package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsOpenAINonBillableRequestError(t *testing.T) {
	tests := []struct {
		name    string
		message string
		body    string
		want    bool
	}{
		{
			name:    "thinking budget must fit max tokens",
			message: "thinking.budget_tokens must be less than max_tokens",
			want:    true,
		},
		{
			name:    "context window full",
			message: "Context window is full for the external model. Reduce conversation history, system prompt, tools, documents, images, or tool results and retry.",
			want:    true,
		},
		{
			name: "context length response payload",
			body: `{"response":{"error":{"code":"context_length_exceeded","message":"input is too long"}}}`,
			want: true,
		},
		{
			name:    "cyber policy remains billable",
			message: "blocked by cyber policy",
			body:    `{"type":"error","error":{"code":"cyber_policy","message":"blocked by cyber policy"}}`,
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isOpenAINonBillableRequestError(tt.message, []byte(tt.body)))
		})
	}
}

func TestRecordUsage_NonBillableUpstreamRequestErrorDoesNotDeduct(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{
			RequestID:                "resp_context_rejected",
			Usage:                    OpenAIUsage{InputTokens: 100000},
			Model:                    "gpt-5.1",
			Duration:                 time.Second,
			NonBillableUpstreamError: true,
		},
		APIKey:  &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2000},
		Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 100000, usageRepo.lastLog.InputTokens, "retain usage for diagnostics")
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.NotNil(t, billingRepo.lastCmd)
	require.Zero(t, billingRepo.lastCmd.BalanceCost)
}
