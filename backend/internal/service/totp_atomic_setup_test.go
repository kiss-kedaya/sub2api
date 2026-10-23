//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

type atomicTotpSetupRepo struct {
	UserRepository
	mu        sync.Mutex
	user      User
	bindErr   error
	bindCalls int
}

func (r *atomicTotpSetupRepo) GetByID(context.Context, int64) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	user := r.user
	return &user, nil
}

func (r *atomicTotpSetupRepo) BindTotpIfDisabled(_ context.Context, _ int64, secret string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindCalls++
	if r.bindErr != nil {
		return r.bindErr
	}
	if r.user.TotpEnabled || r.user.Status != StatusActive {
		return ErrUserSecurityStateChanged
	}
	r.user.TotpEnabled = true
	r.user.TotpSecretEncrypted = &secret
	now := time.Now()
	r.user.TotpEnabledAt = &now
	return nil
}

type atomicTotpSetupCache struct {
	TotpCache
	session  *TotpSetupSession
	deleted  atomic.Int64
	attempts atomic.Int64
}

func (c *atomicTotpSetupCache) GetVerifyAttempts(context.Context, int64) (int, error) {
	return int(c.attempts.Load()), nil
}

func (c *atomicTotpSetupCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return int(c.attempts.Add(1)), nil
}

func (c *atomicTotpSetupCache) ClearVerifyAttempts(context.Context, int64) error {
	c.attempts.Store(0)
	return nil
}

func (c *atomicTotpSetupCache) GetSetupSession(context.Context, int64) (*TotpSetupSession, error) {
	session := *c.session
	return &session, nil
}

func (c *atomicTotpSetupCache) DeleteSetupSession(context.Context, int64) error {
	c.deleted.Add(1)
	return nil
}

type atomicTotpSetupEncryptor struct {
	arrived chan struct{}
	resume  chan struct{}
}

func (e atomicTotpSetupEncryptor) Encrypt(secret string) (string, error) {
	if e.arrived != nil {
		e.arrived <- struct{}{}
		<-e.resume
	}
	return "encrypted:" + secret, nil
}

func (atomicTotpSetupEncryptor) Decrypt(string) (string, error) {
	panic("setup must not decrypt or log the new secret")
}

func newAtomicTotpSetupService(repo UserRepository, encryptor SecretEncryptor) (*TotpService, *atomicTotpSetupCache, string) {
	const secret = "JBSWY3DPEHPK3PXP"
	cache := &atomicTotpSetupCache{session: &TotpSetupSession{Secret: secret, SetupToken: "setup-token", CreatedAt: time.Now()}}
	settings := NewSettingService(&totpVMSettingRepoStub{values: map[string]string{SettingKeyTotpEnabled: "true"}}, nil)
	return NewTotpService(repo, encryptor, cache, settings, nil, nil), cache, secret
}

func TestTotpCompleteSetupConcurrentRequestsOnlyBindOnce(t *testing.T) {
	repo := &atomicTotpSetupRepo{user: User{ID: 42, Role: RoleAdmin, Status: StatusActive}}
	arrived := make(chan struct{}, 2)
	resume := make(chan struct{})
	svc, cache, secret := newAtomicTotpSetupService(repo, atomicTotpSetupEncryptor{arrived: arrived, resume: resume})
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- svc.CompleteSetup(context.Background(), 42, code, "setup-token") }()
	}
	<-arrived
	<-arrived
	close(resume)
	winners := 0
	for range 2 {
		if err := <-results; err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrUserSecurityStateChanged)
		}
	}
	require.Equal(t, 1, winners)
	// Each verified request consumes the cached setup before writing; the DB
	// precondition remains decisive when both already read that setup.
	require.EqualValues(t, 2, cache.deleted.Load())
	require.True(t, repo.user.TotpEnabled)
	require.NotNil(t, repo.user.TotpSecretEncrypted)
	require.Equal(t, "encrypted:"+secret, *repo.user.TotpSecretEncrypted)
}

func TestTotpCompleteSetupStaleSessionCannotReplaceEnabledAuthenticator(t *testing.T) {
	original := "current-authenticator-secret"
	repo := &atomicTotpSetupRepo{user: User{ID: 42, Role: RoleAdmin, Status: StatusActive, TotpEnabled: true, TotpSecretEncrypted: &original}}
	svc, cache, secret := newAtomicTotpSetupService(repo, atomicTotpSetupEncryptor{})
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, svc.CompleteSetup(context.Background(), 42, code, "setup-token"), ErrTotpAlreadyEnabled)
	require.True(t, repo.user.TotpEnabled)
	require.Equal(t, original, *repo.user.TotpSecretEncrypted)
	require.Zero(t, cache.deleted.Load())
}

func TestTotpCompleteSetupFailsClosedWithoutAtomicBinder(t *testing.T) {
	svc, cache, secret := newAtomicTotpSetupService(&totpVMUserRepoStub{user: &User{ID: 42, Role: RoleAdmin, Status: StatusActive}}, atomicTotpSetupEncryptor{})
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, svc.CompleteSetup(context.Background(), 42, code, "setup-token"), ErrUserSecurityStateChanged)
	require.Zero(t, cache.deleted.Load())
}

func TestTotpCompleteSetupDatabaseErrorDoesNotMarkBoundAndConsumesSetup(t *testing.T) {
	writeError := errors.New("database write failed")
	repo := &atomicTotpSetupRepo{user: User{ID: 42, Role: RoleAdmin, Status: StatusActive}, bindErr: writeError}
	svc, cache, secret := newAtomicTotpSetupService(repo, atomicTotpSetupEncryptor{})
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, svc.CompleteSetup(context.Background(), 42, code, "setup-token"), writeError)
	require.False(t, repo.user.TotpEnabled)
	require.Nil(t, repo.user.TotpSecretEncrypted)
	require.Nil(t, repo.user.TotpEnabledAt)
	// Fail closed: a setup capability is consumed before the database write.
	require.EqualValues(t, 1, cache.deleted.Load())
}
