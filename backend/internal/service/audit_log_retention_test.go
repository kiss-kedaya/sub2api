package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type auditRetentionSettingsFixture struct {
	SettingRepository
	value string
	err   error
	reads int
}

func (r *auditRetentionSettingsFixture) GetValueUncached(_ context.Context, key string) (string, error) {
	if key != SettingKeyAuditLogRetentionDays {
		return "", errors.New("unexpected setting in audit retention test")
	}
	r.reads++
	return r.value, r.err
}

func (*auditRetentionSettingsFixture) CompareAndSwapValue(context.Context, string, *string, string) (bool, error) {
	return false, errors.New("retention worker must not write settings")
}

type auditRetentionLogFixture struct {
	AuditLogRepository
	cutoffs []time.Time
}

func (r *auditRetentionLogFixture) DeleteBefore(_ context.Context, cutoff time.Time, _ int) (int64, error) {
	r.cutoffs = append(r.cutoffs, cutoff)
	return 0, nil
}

func TestAuditRetentionRequiresAuthoritativePolicy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    string
		err      error
		wantDays int
	}{
		{name: "read fails", err: errors.New("setting repository unavailable")},
		{name: "invalid policy", value: "not-a-number"},
		{name: "empty stored policy", value: "   "},
		{name: "keep forever", value: "0"},
		{name: "configured retention", value: "365", wantDays: 365},
		{name: "known missing key uses default", err: ErrSettingNotFound, wantDays: defaultAuditLogRetentionDays},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := &auditRetentionSettingsFixture{value: tc.value, err: tc.err}
			repo := &auditRetentionLogFixture{}
			svc := &AuditLogService{ctx: t.Context(), repo: repo, settingService: &SettingService{settingRepo: settings}}
			before := time.Now().UTC().AddDate(0, 0, -tc.wantDays)
			svc.runRetentionOnce()
			require.Equal(t, 1, settings.reads)
			if tc.wantDays == 0 {
				require.Empty(t, repo.cutoffs)
				return
			}
			require.Len(t, repo.cutoffs, 1)
			require.False(t, repo.cutoffs[0].Before(before))
			require.False(t, repo.cutoffs[0].After(time.Now().UTC().AddDate(0, 0, -tc.wantDays)))
		})
	}
}
