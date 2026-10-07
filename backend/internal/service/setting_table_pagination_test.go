package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type tablePaginationRepoStub struct {
	SettingRepository
	values map[string]string
	calls  int
	err    error
}

func (r *tablePaginationRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	r.calls++
	return r.values, r.err
}

func TestTablePaginationSettingsCacheAndPublication(t *testing.T) {
	repo := &tablePaginationRepoStub{values: map[string]string{
		SettingKeyTableDefaultPageSize: "1000",
		SettingKeyTablePageSizeOptions: "[50,10,20,50]",
	}}
	svc := &SettingService{settingRepo: repo}
	for range 3 {
		def, max := svc.GetTablePaginationLimits(context.Background())
		require.Equal(t, 50, def)
		require.Equal(t, 50, max)
	}
	require.Equal(t, 1, repo.calls)
	svc.publishTablePaginationLimits(10, []int{5, 10})
	def, max := svc.GetTablePaginationLimits(context.Background())
	require.Equal(t, 10, def)
	require.Equal(t, 10, max)
	require.Equal(t, 1, repo.calls)

	svc.tablePaginationCache.Store(&cachedTablePaginationLimits{10, 10, time.Now().Add(-time.Second)})
	repo.err = errors.New("storage unavailable")
	def, max = svc.GetTablePaginationLimits(context.Background())
	require.Equal(t, 10, def)
	require.Equal(t, 10, max)
	require.Equal(t, 2, repo.calls)
}

func TestTablePaginationSettingsCacheRefresh(t *testing.T) {
	repo := &tablePaginationRepoStub{values: map[string]string{
		SettingKeyTableDefaultPageSize: "10",
		SettingKeyTablePageSizeOptions: "[5,10]",
	}}
	svc := &SettingService{settingRepo: repo}
	svc.tablePaginationCache.Store(&cachedTablePaginationLimits{20, 100, time.Now().Add(-time.Second)})
	def, max := svc.GetTablePaginationLimits(context.Background())
	require.Equal(t, 10, def)
	require.Equal(t, 10, max)
	require.Equal(t, 1, repo.calls)
}

func TestTablePaginationSettingsColdFailureIsConservative(t *testing.T) {
	svc := &SettingService{settingRepo: &tablePaginationRepoStub{err: errors.New("storage unavailable")}}
	def, max := svc.GetTablePaginationLimits(context.Background())
	require.Equal(t, 5, def)
	require.Equal(t, 5, max)
}

func TestValidateTablePreferences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		def     int
		options []int
	}{
		{"zero default", 0, []int{10}},
		{"negative default", -1, []int{10}},
		{"small default", 4, []int{10}},
		{"large default", 1001, []int{10}},
		{"empty options", 20, []int{}},
		{"small option", 20, []int{4, 10}},
		{"large option", 20, []int{10, 1001}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, ValidateTablePreferences(tc.def, tc.options))
		})
	}
	require.NoError(t, ValidateTablePreferences(1000, []int{1000, 5, 5}))
}
