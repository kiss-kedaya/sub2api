package repository

import (
	"context"
	"fmt"
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
		codexOverdraftRateLimitPredicate(),
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

// codexOverdraftRateLimitPredicate keeps the normal rate-limit exclusion for
// ordinary 429s, while admitting an account whose server-persisted Codex
// 5-hour/7-day snapshot is already in the overdraft pre-arm window. The JSON
// values are written as numbers by the quota parser; the service-level filter
// performs the same check defensively for legacy/string snapshots.
func codexOverdraftRateLimitPredicate() dbpredicate.Account {
	return dbpredicate.Account(func(s *entsql.Selector) {
		rateReset := s.C(dbaccount.FieldRateLimitResetAt)
		extra := s.C(dbaccount.FieldExtra)
		quotaWindow := func(usedKey, resetAfterKey, resetAtKey string) string {
			// pg_input_is_valid keeps malformed legacy/string snapshots from
			// turning candidate discovery into a 500 via an unsafe cast. CASE is
			// intentional: PostgreSQL may reorder boolean predicates, but it will
			// not evaluate the cast in the false CASE branch.
			numeric := func(key string, max string) string {
				value := fmt.Sprintf("%s->>'%s'", extra, key)
				return fmt.Sprintf("(CASE WHEN pg_input_is_valid(%s, 'double precision') THEN (CASE WHEN %s ~ '^[0-9]+(\\.[0-9]+)?$' THEN (CASE WHEN (%s)::double precision BETWEEN 0 AND %s THEN (%s)::double precision ELSE NULL END) ELSE NULL END) ELSE NULL END)", value, value, value, max, value)
			}
			timestampValue := fmt.Sprintf("%s->>'%s'", extra, resetAtKey)
			timestamp := fmt.Sprintf("(CASE WHEN pg_input_is_valid(%s, 'timestamptz') THEN (CASE WHEN isfinite((%s)::timestamptz) THEN (%s)::timestamptz ELSE NULL END) ELSE NULL END)", timestampValue, timestampValue, timestampValue)
			updatedValue := fmt.Sprintf("%s->>'codex_usage_updated_at'", extra)
			updatedAt := fmt.Sprintf("(CASE WHEN pg_input_is_valid(%s, 'timestamptz') THEN (CASE WHEN isfinite((%s)::timestamptz) THEN (%s)::timestamptz ELSE NULL END) ELSE NULL END)", updatedValue, updatedValue, updatedValue)
			after := numeric(resetAfterKey, "315576000")
			futureAfter := fmt.Sprintf("(%s IS NOT NULL AND %s + make_interval(secs => %s) > NOW())", updatedAt, updatedAt, after)
			return fmt.Sprintf("(%s >= 95 AND ((%s IS NOT NULL AND %s > NOW()) OR (%s IS NULL AND %s)))", numeric(usedKey, "1000"), timestamp, timestamp, timestamp, futureAfter)
		}
		s.Where(entsql.Or(
			entsql.IsNull(rateReset),
			entsql.LTE(rateReset, entsql.Expr("NOW()")),
			entsql.And(
				entsql.GT(rateReset, entsql.Expr("NOW()")),
				entsql.Or(
					entsql.P(func(b *entsql.Builder) {
						b.WriteString(quotaWindow("codex_5h_used_percent", "codex_5h_reset_after_seconds", "codex_5h_reset_at"))
					}),
					entsql.P(func(b *entsql.Builder) {
						b.WriteString(quotaWindow("codex_7d_used_percent", "codex_7d_reset_after_seconds", "codex_7d_reset_at"))
					}),
				),
			),
		))
	})
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
