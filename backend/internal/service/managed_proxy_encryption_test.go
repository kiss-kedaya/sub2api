package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

type managedProxyFixtureRepository struct {
	ProxyRepository
	row        Proxy
	references int64
	deleted    int
	deleteErr  error
}

func (r *managedProxyFixtureRepository) CountAccountsByProxyID(context.Context, int64) (int64, error) {
	return r.references, nil
}

func (r *managedProxyFixtureRepository) Delete(context.Context, int64) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deleted++
	return nil
}

func (r *managedProxyFixtureRepository) Create(_ context.Context, p *Proxy) error {
	p.ID = 7
	r.row = *p
	return nil
}
func (r *managedProxyFixtureRepository) Update(_ context.Context, p *Proxy) error {
	r.row = *p
	return nil
}
func (r *managedProxyFixtureRepository) GetByID(context.Context, int64) (*Proxy, error) {
	copy := r.row
	return &copy, nil
}

type managedProxyFixtureEncryptor struct{ fail bool }

func (e managedProxyFixtureEncryptor) Encrypt(s string) (string, error) {
	if e.fail {
		return "", errors.New("fixture")
	}
	return "sealed:" + s, nil
}
func (e managedProxyFixtureEncryptor) Decrypt(s string) (string, error) {
	if e.fail || !strings.HasPrefix(s, "sealed:") {
		return "", errors.New("fixture")
	}
	return strings.TrimPrefix(s, "sealed:"), nil
}
func TestManagedProxyCredentialsEncryptedAtRestAndPlainOnlyInMemory(t *testing.T) {
	store := &managedProxyFixtureRepository{}
	repo := WrapEncryptedManagedProxyRepository(store, managedProxyFixtureEncryptor{})
	p := &Proxy{Username: "mh_fixture", Password: "sensitive-fixture", Host: "10.0.0.1", Port: 32001}
	require.NoError(t, repo.Create(context.Background(), p))
	require.Equal(t, int64(7), p.ID)
	require.Equal(t, "sensitive-fixture", p.Password)
	require.Equal(t, managedProxyCipherPrefix+"sealed:sensitive-fixture", store.row.Password)
	read, err := repo.GetByID(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "sensitive-fixture", read.Password)
	require.NoError(t, repo.Update(context.Background(), read))
	require.Equal(t, managedProxyCipherPrefix+"sealed:sensitive-fixture", store.row.Password)
}
func TestHistoricalProxyRemainsCompatibleWithoutEncryptor(t *testing.T) {
	store := &managedProxyFixtureRepository{}
	repo := WrapEncryptedManagedProxyRepository(store, nil)
	p := &Proxy{Username: "ordinary", Password: "legacy"}
	require.NoError(t, repo.Create(context.Background(), p))
	read, err := repo.GetByID(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy", read.Password)
}
func TestManagedProxyDecryptionFailureCannotBecomeOutboundCredential(t *testing.T) {
	store := &managedProxyFixtureRepository{row: Proxy{Username: "mh_fixture", Password: managedProxyCipherPrefix + "invalid"}}
	for _, encryptor := range []SecretEncryptor{nil, managedProxyFixtureEncryptor{fail: true}} {
		repo := WrapEncryptedManagedProxyRepository(store, encryptor)
		read, err := repo.GetByID(context.Background(), 7)
		require.Error(t, err)
		require.Nil(t, read)
		require.NotContains(t, err.Error(), "invalid")
	}
}

func TestManagedProxyCannotDeleteRowWithoutReleasingListener(t *testing.T) {
	store := &managedProxyFixtureRepository{row: Proxy{ID: 7, Username: "mh_fixture", Password: managedProxyCipherPrefix + "sealed:fixture"}}
	repo := WrapEncryptedManagedProxyRepository(store, managedProxyFixtureEncryptor{})
	err := repo.Delete(context.Background(), 7)
	require.Error(t, err)
	require.Zero(t, store.deleted)
	require.NoError(t, repo.Delete(withManagedProxyDeletion(context.Background()), 7))
	require.Equal(t, 1, store.deleted)
	store.row = Proxy{ID: 8, Username: "ordinary", Password: "legacy"}
	require.NoError(t, repo.Delete(context.Background(), 8))
	require.Equal(t, 2, store.deleted)
}
