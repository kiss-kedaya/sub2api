//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpstreamFinancialPersistedBatchItems(t *testing.T) {
	svc, repo, _, _, _ := newTestBatchImagePublicService(true)
	keyID := int64(22)
	repo.jobs["imgbatch_financial"] = &BatchImageJob{BatchID: "imgbatch_financial", UserID: 11, APIKeyID: &keyID, Status: BatchImageJobStatusCompleted, CreatedAt: time.Now()}
	source := "gs://private-bucket/vendor/output.jsonl"
	code := "insufficient_quota"
	message := "当前额度: 0.1, 需要额度: 1.64 vendor-A host.internal sk-secret"
	repo.items["imgbatch_financial"] = []CreateBatchImageItemParams{{JobID: "imgbatch_financial", CustomID: "request_1", Status: BatchImageItemStatusFailed, ProviderSourceObject: &source, ErrorCode: &code, ErrorMessage: &message}}
	result, err := svc.ListItems(context.Background(), testBatchImageOwner(), "imgbatch_financial", BatchImageItemsQuery{Status: "failed"})
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	require.Equal(t, "request_1", result.Data[0].CustomID)
	require.Equal(t, "failed", result.Data[0].Status)
	require.Equal(t, &BatchImagePublicError{Code: "upstream_error", Message: UpstreamUnavailableMessage}, result.Data[0].Error)
	body, err := json.Marshal(result)
	require.NoError(t, err)
	for _, secret := range []string{"sk-secret", "host.internal", "vendor-A", "1.64", "insufficient_quota"} {
		require.NotContains(t, string(body), secret)
	}
	require.Equal(t, message, *repo.items["imgbatch_financial"][0].ErrorMessage)
	require.Equal(t, code, *repo.items["imgbatch_financial"][0].ErrorCode)
	_, err = svc.ListItems(context.Background(), BatchImageOwner{UserID: 12, APIKeyID: 99}, "imgbatch_financial", BatchImageItemsQuery{})
	require.ErrorIs(t, err, ErrBatchImageJobNotFound)
}

func TestUpstreamFinancialBatchLocalAndSuccessBoundaries(t *testing.T) {
	for _, test := range []struct{ status, source, code, message string }{
		{BatchImageItemStatusFailed, "", "BILLING_SERVICE_ERROR", "Billing service temporarily unavailable"},
		{BatchImageItemStatusFailed, "gs://private", "INVALID_ARGUMENT", "Invalid usage field"},
		{BatchImageItemStatusSuccess, "gs://private", "insufficient_quota", "当前额度: 0, 需要额度: 1.64"},
		{BatchImageItemStatusPending, "gs://private", "insufficient_quota", "当前额度: 0, 需要额度: 1.64"},
	} {
		item := &BatchImageItem{CustomID: "item_1", Status: test.status, ProviderSourceObject: &test.source, ErrorCode: &test.code, ErrorMessage: &test.message}
		public := BatchImageItemToPublic(item)
		if test.status == BatchImageItemStatusFailed {
			require.Equal(t, test.code, public.Error.Code)
			require.Equal(t, test.message, public.Error.Message)
		} else {
			require.Nil(t, public.Error)
		}
	}
}
