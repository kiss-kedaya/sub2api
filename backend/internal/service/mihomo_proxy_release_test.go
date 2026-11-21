package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/stretchr/testify/require"
)

type mihomoReleaseFixtureBindings struct{}

func (mihomoReleaseFixtureBindings) WithHostLock(_ context.Context, _ string, run func() error) error {
	return run()
}
func (mihomoReleaseFixtureBindings) GetProxyID(context.Context, string, string) (int64, error) {
	return 7, nil
}
func (mihomoReleaseFixtureBindings) Save(context.Context, string, string, int64) error { return nil }

type mihomoReleaseFixtureKernel struct{ closed, restored int }

func (k *mihomoReleaseFixtureKernel) BindStaticNodes(context.Context, []string) ([]mihomo.StaticNodeBinding, error) {
	k.restored++
	return nil, nil
}
func (k *mihomoReleaseFixtureKernel) DeactivateStaticNodes(context.Context, []string) error {
	k.closed++
	return nil
}
func (*mihomoReleaseFixtureKernel) HostID() string { return "host-fixture" }

func TestManagedProxyReleaseProtectsInUseAndRestoresFailedDeletion(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		references                int64
		deleteErr                 error
		wantError                 bool
		closed, restored, deleted int
	}{
		{name: "in use", references: 1, wantError: true},
		{name: "delete failed", deleteErr: errors.New("fixture database unavailable"), wantError: true, closed: 1, restored: 1},
		{name: "released", closed: 1, deleted: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &managedProxyFixtureRepository{row: Proxy{ID: 7, Username: "mh_fixture", Password: managedProxyCipherPrefix + "sealed:fixture"}, references: tc.references, deleteErr: tc.deleteErr}
			repo := WrapEncryptedManagedProxyRepository(store, managedProxyFixtureEncryptor{})
			admin := &adminServiceImpl{proxyRepo: repo}
			svc := NewMihomoProxyService(admin, repo, mihomoReleaseFixtureBindings{})
			kernel := &mihomoReleaseFixtureKernel{}
			n, err := svc.RemoveMihomoNodeProxies(context.Background(), kernel, []string{"node-one", "node-one"})
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, n)
			}
			require.Equal(t, tc.closed, kernel.closed)
			require.Equal(t, tc.restored, kernel.restored)
			require.Equal(t, tc.deleted, store.deleted)
		})
	}
}
