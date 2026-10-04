package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGroupQualityChanshuiSummaryPreservesHistoricalMethod(t *testing.T) {
	e := &GroupQualityEvent{Status: "success"}
	PopulateGroupQualityAudit(e, `{"provider":"chanshui"}`)
	require.Equal(t, QualityProviderPelican, e.QualityProvider)
	require.Nil(t, e.AuditSummary)
}

func TestGroupQualityChanshuiSummaryRedactsUnknownAndSensitiveFields(t *testing.T) {
	e := &GroupQualityEvent{Status: "audit_fail", ErrorMessage: "private reason"}
	PopulateGroupQualityAudit(e, `{"provider":"chanshui","audit_id":"private-id","verdict":{"total":{"score":56},"fingerprint":{"top_model":"sk-fixture-sensitive-model"},"tools":{"status":"pass","reason":"private response"},"evil":{"score":100}},"probes":[{"api_key":"private-key"}]}`)
	require.Equal(t, "degraded", e.Status)
	require.Equal(t, QualityProviderChanshui, e.QualityProvider)
	require.Empty(t, e.AuditSummary.CandidateModel)
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	for _, private := range []string{"private", "sk-fixture", "probes", "evil", "audit_id", "api_key"} {
		require.NotContains(t, string(raw), private)
	}
}

func TestGroupQualityStatusCountsAuditVerdicts(t *testing.T) {
	repo := newStubGroupQualityCheckRepo()
	repo.settings[43] = &GroupQualityCheckSettings{GroupID: 43, Enabled: true}
	repo.addResult(43, 1, "audit_pass", time.Now())
	repo.addResult(43, 2, "audit_fail", time.Now())
	status, err := NewGroupQualityCheckService(repo, nil).GetGroupStatus(context.Background(), 43)
	require.NoError(t, err)
	require.Equal(t, 2, status.CheckedAccounts)
	require.Equal(t, 1, status.DegradedAccounts)
}
