package service

import (
	"context"
	"time"
)

// The PostgreSQL repository can read monitor snapshots in one query without
// hydrating routing groups and proxies. Other repositories retain their existing
// lookup behavior, including per-account failures.
type accountOpsSnapshotReader interface {
	ListAccountOpsSnapshots(context.Context, []int64) ([]Account, error)
}

func (s *AccountOpsService) listAccountOpsAccounts(ctx context.Context) ([]Account, error) {
	if reader, ok := s.accounts.(accountOpsSnapshotReader); ok {
		return reader.ListAccountOpsSnapshots(ctx, nil)
	}
	return s.accounts.ListAllWithFilters(ctx, "", "", "", "", 0, "")
}

func (s *AccountOpsService) loadThresholdAccounts(ctx context.Context, ids []int64) map[int64]accountOpsThresholdMember {
	members := make(map[int64]accountOpsThresholdMember, len(ids))
	if len(ids) == 0 {
		return members
	}
	timeout := s.thresholdLookupTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if reader, ok := s.accounts.(accountOpsSnapshotReader); ok {
		query, cancel := context.WithTimeout(ctx, timeout)
		accounts, err := reader.ListAccountOpsSnapshots(query, ids)
		cancel()
		for _, id := range ids {
			lookupErr := err
			if lookupErr == nil {
				lookupErr = ErrAccountNotFound
			}
			members[id] = accountOpsThresholdMember{id: id, lookupErr: lookupErr}
		}
		// A failed batch is unknown even if a reader also returns partial data.
		if err == nil {
			for i := range accounts {
				account := &accounts[i]
				members[account.ID] = accountOpsThresholdMember{id: account.ID, account: account}
			}
		}
		return members
	}
	for _, id := range ids {
		query, cancel := context.WithTimeout(ctx, timeout)
		account, err := s.accounts.GetByID(query, id)
		cancel()
		members[id] = accountOpsThresholdMember{id: id, account: account, lookupErr: err}
	}
	return members
}
