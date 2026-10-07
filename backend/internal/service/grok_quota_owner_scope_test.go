package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type grokQuotaOwnerScopeRepo struct {
	AccountRepository
	scopedReads []bool
}

type grokQuotaOwnerScopeUpstream struct{ HTTPUpstream }

func (r *grokQuotaOwnerScopeRepo) GetByID(ctx context.Context, _ int64) (*Account, error) {
	_, _, scoped := AccountOwnerScopeDetail(ctx)
	r.scopedReads = append(r.scopedReads, scoped)
	return nil, ErrAccountNotFound
}

func TestGrokQuotaAuthorizesBeforeDetachedSharedProbe(t *testing.T) {
	for _, kind := range []string{"billing", "usage"} {
		t.Run(kind, func(t *testing.T) {
			repo := &grokQuotaOwnerScopeRepo{}
			svc := NewGrokQuotaService(repo, nil, &GrokTokenProvider{}, &grokQuotaOwnerScopeUpstream{}, nil)
			ctx := WithAccountOwnerScope(context.Background(), 7, 0)
			var err error
			if kind == "billing" {
				_, err = svc.ProbeBilling(ctx, 12)
			} else {
				_, err = svc.ProbeUsage(ctx, 12)
			}
			require.Error(t, err)
			require.Equal(t, []bool{true}, repo.scopedReads, "deny access before singleflight detaches the administrator context")
		})
	}
}
