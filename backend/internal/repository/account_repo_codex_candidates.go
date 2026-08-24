package repository

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbaccountgroup "github.com/Wei-Shaw/sub2api/ent/accountgroup"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ListCodexQuotaOverdraftCandidates is intentionally separate from the normal
// scheduler projection. Threshold-paused accounts are included only for an
// explicit overdraft request and the result is never written to the shared
// scheduler snapshot.
func (r *accountRepository) ListCodexQuotaOverdraftCandidates(
	ctx context.Context,
	groupID *int64,
	includeUngrouped bool,
) ([]service.Account, error) {
	if r == nil || r.client == nil {
		return nil, nil
	}
	now := time.Now()
	q := r.client.Account.Query().Where(
		dbaccount.DeletedAtIsNil(),
		dbaccount.PlatformEQ(service.PlatformOpenAI),
		dbaccount.TypeEQ(service.AccountTypeOAuth),
		dbaccount.ParentAccountIDIsNil(),
		dbaccount.StatusEQ(service.StatusActive),
		dbaccount.SchedulableEQ(true),
		dbaccount.Or(
			// A live pause is eligible only when it was produced by the normal
			// account scheduling threshold, never for arbitrary admin/runtime pauses.
			dbaccount.TempUnschedulableUntilIsNil(),
			dbaccount.TempUnschedulableUntilLTE(now),
			codexOverdraftThresholdPausePredicate(),
		),
		dbaccount.Or(dbaccount.ExpiresAtIsNil(), dbaccount.ExpiresAtGT(now), dbaccount.AutoPauseOnExpiredEQ(false)),
		dbaccount.Or(dbaccount.OverloadUntilIsNil(), dbaccount.OverloadUntilLTE(now)),
		dbaccount.Or(dbaccount.RateLimitResetAtIsNil(), dbaccount.RateLimitResetAtLTE(now)),
	)
	if groupID != nil && *groupID > 0 {
		q = q.Where(dbaccount.HasAccountGroupsWith(dbaccountgroup.GroupIDEQ(*groupID)))
	} else if includeUngrouped {
		q = q.Where(dbaccount.Not(dbaccount.HasAccountGroups()))
	}
	accounts, err := q.Order(dbent.Asc(dbaccount.FieldPriority)).Limit(256).All(ctx)
	if err != nil {
		return nil, err
	}
	converted, err := r.accountsToService(ctx, accounts)
	if err != nil {
		return nil, err
	}
	filtered := converted[:0]
	for i := range converted {
		if converted[i].TempUnschedulableUntil != nil && converted[i].TempUnschedulableUntil.After(now) &&
			!service.IsAccountSchedulingThresholdReason(converted[i].TempUnschedulableReason) {
			continue
		}
		filtered = append(filtered, converted[i])
	}
	return filtered, nil
}

func codexOverdraftThresholdPausePredicate() dbpredicate.Account {
	return dbpredicate.Account(func(s *entsql.Selector) {
		reason := s.C("temp_unschedulable_reason")
		s.Where(entsql.And(
			entsql.Not(entsql.IsNull(s.C("temp_unschedulable_until"))),
			entsql.GT(s.C("temp_unschedulable_until"), entsql.Expr("NOW()")),
			entsql.Contains(reason, service.AccountSchedulingThresholdReasonSource),
		))
	})
}
