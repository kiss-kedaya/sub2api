package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
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

func proxyTestVault(t *testing.T) SecretVault {
	t.Helper()
	return NewSecretVault(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("42", 32)}})
}

func TestManagedProxyCredentialsEncryptedAtRestAndPlainOnlyInMemory(t *testing.T) {
	store := &managedProxyFixtureRepository{}
	vault := proxyTestVault(t)
	repo := WrapEncryptedProxyRepository(store, vault, nil)
	p := &Proxy{Username: "mh_fixture", Password: "sensitive-fixture", Host: "10.0.0.1", Port: 32001}
	require.NoError(t, repo.Create(context.Background(), p))
	require.Equal(t, int64(7), p.ID)
	require.Equal(t, "sensitive-fixture", p.Password)
	require.True(t, strings.HasPrefix(store.row.Password, ProxyPasswordCipherPrefix))
	require.NotContains(t, store.row.Password, "sensitive-fixture")

	read, err := repo.GetByID(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "sensitive-fixture", read.Password)
	require.NoError(t, repo.Update(context.Background(), read))
	require.True(t, strings.HasPrefix(store.row.Password, ProxyPasswordCipherPrefix))
}

func TestOrdinaryProxyPasswordIsEncryptedToo(t *testing.T) {
	store := &managedProxyFixtureRepository{}
	vault := proxyTestVault(t)
	repo := WrapEncryptedProxyRepository(store, vault, nil)
	p := &Proxy{Username: "ordinary", Password: "legacy"}
	require.NoError(t, repo.Create(context.Background(), p))
	require.True(t, strings.HasPrefix(store.row.Password, ProxyPasswordCipherPrefix))
	read, err := repo.GetByID(context.Background(), p.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy", read.Password)
}

func TestHistoricalPlaintextProxyRemainsCompatibleWithoutKey(t *testing.T) {
	store := &managedProxyFixtureRepository{row: Proxy{ID: 9, Username: "ordinary", Password: "legacy-plain"}}
	repo := WrapEncryptedProxyRepository(store, NewSecretVault(nil), nil)
	read, err := repo.GetByID(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, "legacy-plain", read.Password)
}

func TestProxyDecryptionFailureCannotBecomeOutboundCredential(t *testing.T) {
	encrypted, err := proxyTestVault(t).EncryptString("sk-live-secret")
	require.NoError(t, err)
	store := &managedProxyFixtureRepository{row: Proxy{Username: "ordinary", Password: encrypted}}

	// Different key: decryption must fail closed.
	other := NewSecretVault(&config.Config{Totp: config.TotpConfig{EncryptionKey: strings.Repeat("43", 32)}})
	for _, vault := range []SecretVault{other, NewSecretVault(nil)} {
		repo := WrapEncryptedProxyRepository(store, vault, nil)
		read, err := repo.GetByID(context.Background(), 7)
		require.Error(t, err)
		require.Nil(t, read)
		require.NotContains(t, err.Error(), "sk-live-secret")
	}
}

func TestManagedProxyCannotDeleteRowWithoutReleasingListener(t *testing.T) {
	encrypted, err := proxyTestVault(t).EncryptString("fixture")
	require.NoError(t, err)
	store := &managedProxyFixtureRepository{row: Proxy{ID: 7, Username: "mh_fixture", Password: encrypted}}
	repo := WrapEncryptedProxyRepository(store, proxyTestVault(t), nil)
	err = repo.Delete(context.Background(), 7)
	require.Error(t, err)
	require.Zero(t, store.deleted)
	require.NoError(t, repo.Delete(withManagedProxyDeletion(context.Background()), 7))
	require.Equal(t, 1, store.deleted)

	ordinary, err := proxyTestVault(t).EncryptString("legacy")
	require.NoError(t, err)
	store.row = Proxy{ID: 8, Username: "ordinary", Password: ordinary}
	require.NoError(t, repo.Delete(context.Background(), 8))
	require.Equal(t, 2, store.deleted)
}

// managedProxyFixtureEncryptor is the deterministic SecretEncryptor used by
// unrelated service tests. Production proxy storage now goes through
// SecretVault ("enc:v1:" format); this fixture only keeps those tests compilable.
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

type captureProxyRewriteExecutor struct {
	mu    chan struct{}
	query string
	args  []any
}

func newCaptureProxyRewriteExecutor() *captureProxyRewriteExecutor {
	return &captureProxyRewriteExecutor{mu: make(chan struct{}, 1)}
}

func (e *captureProxyRewriteExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.query = query
	e.args = args
	select {
	case e.mu <- struct{}{}:
	default:
	}
	return proxyRewriteResult{}, nil
}

type proxyRewriteResult struct{}

func (proxyRewriteResult) LastInsertId() (int64, error) { return 0, nil }
func (proxyRewriteResult) RowsAffected() (int64, error) { return 1, nil }

func TestProxyPasswordLazilyRewrittenToCiphertext(t *testing.T) {
	store := &managedProxyFixtureRepository{row: Proxy{ID: 11, Username: "ordinary", Password: "legacy-plain"}}
	vault := proxyTestVault(t)
	exec := newCaptureProxyRewriteExecutor()
	repo := WrapEncryptedProxyRepository(store, vault, exec)

	read, err := repo.GetByID(context.Background(), 11)
	require.NoError(t, err)
	require.Equal(t, "legacy-plain", read.Password)

	select {
	case <-exec.mu:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "lazy proxy password rewrite did not run")
	}
	rewritten, ok := exec.args[0].(string)
	require.True(t, ok)
	require.True(t, strings.HasPrefix(rewritten, ProxyPasswordCipherPrefix))
	require.NotContains(t, rewritten, "legacy-plain")
}
