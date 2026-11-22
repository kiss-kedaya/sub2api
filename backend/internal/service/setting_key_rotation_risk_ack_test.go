package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type riskAckSettingRepoStub struct {
	values map[string]string
	sets   map[string]string
}

func (s *riskAckSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *riskAckSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.values == nil {
		return "", ErrSettingNotFound
	}
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *riskAckSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.sets == nil {
		s.sets = map[string]string{}
	}
	s.sets[key] = value
	return nil
}

func (s *riskAckSettingRepoStub) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

func (s *riskAckSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *riskAckSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *riskAckSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func newRiskAckSettingsService(repo *riskAckSettingRepoStub) *SettingService {
	return &SettingService{settingRepo: repo, cfg: &config.Config{}}
}

func TestGetKeyRotationRiskAckSettings(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   KeyRotationRiskAckSettings
	}{
		{
			name:   "unset falls back to enabled/24h",
			values: nil,
			want:   KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
		},
		{
			name: "stored values are used",
			values: map[string]string{
				SettingKeyKeyRotationRiskAckEnabled: "false",
				SettingKeyKeyRotationRiskAckHours:   "6",
			},
			want: KeyRotationRiskAckSettings{Enabled: false, Hours: 6},
		},
		{
			name: "unparsable values fall back to defaults",
			values: map[string]string{
				SettingKeyKeyRotationRiskAckEnabled: "maybe",
				SettingKeyKeyRotationRiskAckHours:   "-3",
			},
			want: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
		},
		{
			name: "hours above the ceiling fall back to the default",
			values: map[string]string{
				SettingKeyKeyRotationRiskAckEnabled: "true",
				SettingKeyKeyRotationRiskAckHours:   "100000",
			},
			want: KeyRotationRiskAckSettings{Enabled: true, Hours: 24},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newRiskAckSettingsService(&riskAckSettingRepoStub{values: tt.values})
			got, err := svc.GetKeyRotationRiskAckSettings(context.Background())
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSetKeyRotationRiskAckSettings(t *testing.T) {
	repo := &riskAckSettingRepoStub{}
	svc := newRiskAckSettingsService(repo)

	got, err := svc.SetKeyRotationRiskAckSettings(context.Background(), KeyRotationRiskAckSettings{Enabled: false, Hours: 12})
	require.NoError(t, err)
	require.Equal(t, KeyRotationRiskAckSettings{Enabled: false, Hours: 12}, got)
	require.Equal(t, "false", repo.sets[SettingKeyKeyRotationRiskAckEnabled])
	require.Equal(t, "12", repo.sets[SettingKeyKeyRotationRiskAckHours])

	// 缺省 hours 归一到 24。
	_, err = svc.SetKeyRotationRiskAckSettings(context.Background(), KeyRotationRiskAckSettings{Enabled: true})
	require.NoError(t, err)
	require.Equal(t, "true", repo.sets[SettingKeyKeyRotationRiskAckEnabled])
	require.Equal(t, "24", repo.sets[SettingKeyKeyRotationRiskAckHours])

	// 越界直接拒绝，不落库。
	_, err = svc.SetKeyRotationRiskAckSettings(context.Background(), KeyRotationRiskAckSettings{Enabled: true, Hours: 0 - 1})
	require.Error(t, err)
}
