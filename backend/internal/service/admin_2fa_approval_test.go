package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type admin2FAApprovalTestUsers struct {
	user *User
	err  error
}

func (r *admin2FAApprovalTestUsers) GetByID(_ context.Context, id int64) (*User, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.user == nil || r.user.ID != id {
		return nil, nil
	}
	copy := *r.user
	return &copy, nil
}

type admin2FAApprovalTestStore struct {
	mu       sync.Mutex
	digests  map[int64]string
	attempts map[string]int
	ttl      time.Duration
	err      error
}

func (s *admin2FAApprovalTestStore) SetAdmin2FAApproval(_ context.Context, id int64, digest string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.digests[id], s.ttl = digest, ttl
	return nil
}

func (s *admin2FAApprovalTestStore) ConsumeAdmin2FAApproval(_ context.Context, id int64, digest string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	if s.digests[id] != digest {
		return false, nil
	}
	delete(s.digests, id)
	return true, nil
}

func (s *admin2FAApprovalTestStore) IncrementCryptoAttempt(_ context.Context, key string, ttl time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	s.attempts[key]++
	return s.attempts[key], nil
}

func newAdmin2FAApprovalTest(t *testing.T) (*Admin2FAApprovalService, *admin2FAApprovalTestUsers, *admin2FAApprovalTestStore) {
	t.Helper()
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	secret := "test-encrypted-authenticator"
	users := &admin2FAApprovalTestUsers{user: &User{ID: 41, Role: RoleAdmin, Status: StatusActive, TotpEnabled: true, TotpSecretEncrypted: &secret}}
	store := &admin2FAApprovalTestStore{digests: map[int64]string{}, attempts: map[string]int{}}
	return NewAdmin2FAApprovalService(users, store), users, store
}

func TestAdmin2FAApprovalIssueStoresDigestAndConsumesOnlyOnce(t *testing.T) {
	s, _, store := newAdmin2FAApprovalTest(t)
	before := time.Now()
	code, expiresAt, err := s.Issue(context.Background(), 41)
	require.NoError(t, err)
	require.Len(t, code, 64)
	require.NotEqual(t, code, store.digests[41])
	require.NotContains(t, store.digests[41], "authenticator")
	require.Equal(t, Admin2FAApprovalTTL, store.ttl)
	require.WithinDuration(t, before.Add(Admin2FAApprovalTTL), expiresAt, time.Second)
	verified, err := s.Consume(context.Background(), 41, code)
	require.NoError(t, err)
	require.EqualValues(t, 41, verified.ID)
	require.Equal(t, "test-encrypted-authenticator", *verified.TotpSecretEncrypted)
	_, err = s.Consume(context.Background(), 41, code)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalInvalid)
}

func TestAdmin2FAApprovalReissueAndAuthenticatorBinding(t *testing.T) {
	s, users, _ := newAdmin2FAApprovalTest(t)
	oldCode, _, err := s.Issue(context.Background(), 41)
	require.NoError(t, err)
	newCode, _, err := s.Issue(context.Background(), 41)
	require.NoError(t, err)
	require.NotEqual(t, oldCode, newCode)
	_, err = s.Consume(context.Background(), 41, oldCode)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalInvalid)
	_, err = s.Consume(context.Background(), 42, newCode)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalInvalid)
	newSecret := "replacement-authenticator"
	users.user.TotpSecretEncrypted = &newSecret
	_, err = s.Consume(context.Background(), 41, newCode)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalInvalid)
}

func TestAdmin2FAApprovalRechecksAccountStateAndFailsClosed(t *testing.T) {
	for _, state := range []string{"disabled", "demoted", "deleted", "totp_disabled", "secret_missing", "secret_empty", "database_error", "redis_error"} {
		t.Run(state, func(t *testing.T) {
			s, users, store := newAdmin2FAApprovalTest(t)
			code, _, err := s.Issue(context.Background(), 41)
			require.NoError(t, err)
			want := ErrAdmin2FAApprovalInvalid
			switch state {
			case "disabled":
				users.user.Status = "disabled"
			case "demoted":
				users.user.Role = RoleUser
			case "deleted":
				now := time.Now()
				users.user.DeletedAt = &now
			case "totp_disabled":
				users.user.TotpEnabled = false
			case "secret_missing":
				users.user.TotpSecretEncrypted = nil
			case "secret_empty":
				empty := ""
				users.user.TotpSecretEncrypted = &empty
			case "database_error":
				users.err = errors.New("PRIVATE database password")
				want = ErrAdmin2FAApprovalUnavailable
			case "redis_error":
				store.err = errors.New("PRIVATE Redis password")
				want = ErrAdmin2FAApprovalUnavailable
			}
			_, err = s.Consume(context.Background(), 41, code)
			require.ErrorIs(t, err, want)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.NotContains(t, err.Error(), code)
		})
	}
}

func TestAdmin2FAApprovalRateLimitIncludesMalformedCodes(t *testing.T) {
	s, _, _ := newAdmin2FAApprovalTest(t)
	code, _, err := s.Issue(context.Background(), 41)
	require.NoError(t, err)
	for i := 0; i < admin2FAApprovalMaxAttempts; i++ {
		_, err = s.Consume(context.Background(), 41, "not-a-server-code")
		require.ErrorIs(t, err, ErrAdmin2FAApprovalInvalid)
	}
	_, err = s.Consume(context.Background(), 41, code)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalRateLimited)
}

func TestAdmin2FAApprovalParallelConsumeAllowsOneWinner(t *testing.T) {
	s, _, _ := newAdmin2FAApprovalTest(t)
	code, _, err := s.Issue(context.Background(), 41)
	require.NoError(t, err)
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Consume(context.Background(), 41, code); err == nil {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, won.Load())
}

func TestAdmin2FAApprovalReadonlyRefusesBeforeAnyDependencies(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	s := NewAdmin2FAApprovalService(nil, nil)
	_, _, err := s.Issue(context.Background(), 41)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalReadOnly)
	_, err = s.Consume(context.Background(), 41, strings.Repeat("a", 64))
	require.ErrorIs(t, err, ErrAdmin2FAApprovalReadOnly)
}

func TestAdmin2FAApprovalIssueRefusesWriteEnabledTestInstances(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "true")
	_, _, err := NewAdmin2FAApprovalService(nil, nil).Issue(context.Background(), 41)
	require.ErrorIs(t, err, ErrAdmin2FAApprovalReadOnly)
}
