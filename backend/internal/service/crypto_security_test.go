//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

const cryptoSecurityTestSecret = "JBSWY3DPEHPK3PXP"

// Shared by separate service instances; repository tests exercise the Redis
// implementation. The fake keeps atomic semantics and never accesses a DB.
type cryptoSecurityTestCache struct {
	TotpCache
	mu       sync.Mutex
	setup    *TotpSetupSession
	attempts int
	values   map[string]string
	counts   map[string]int
	fail     bool
}

func (c *cryptoSecurityTestCache) GetSetupSession(context.Context, int64) (*TotpSetupSession, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.setup == nil {
		return nil, nil
	}
	session := *c.setup
	return &session, nil
}

func (c *cryptoSecurityTestCache) SetSetupSession(_ context.Context, _ int64, session *TotpSetupSession, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := *session
	c.setup = &copy
	return nil
}

func (c *cryptoSecurityTestCache) DeleteSetupSession(context.Context, int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setup = nil
	return nil
}

func (c *cryptoSecurityTestCache) GetVerifyAttempts(context.Context, int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts, nil
}

func (c *cryptoSecurityTestCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	return c.attempts, nil
}

func (c *cryptoSecurityTestCache) ClearVerifyAttempts(context.Context, int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts = 0
	return nil
}

func (c *cryptoSecurityTestCache) IncrementCryptoAttempt(_ context.Context, key string, _ time.Duration) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return 0, errors.New("store unavailable")
	}
	c.counts[key]++
	return c.counts[key], nil
}

func (c *cryptoSecurityTestCache) SetCryptoValueIfAbsent(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return false, errors.New("store unavailable")
	}
	if _, exists := c.values[key]; exists {
		return false, nil
	}
	c.values[key] = value
	return true, nil
}

func (c *cryptoSecurityTestCache) CryptoValueExists(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.values[key]
	return exists, nil
}

func (c *cryptoSecurityTestCache) ReleaseCryptoValue(_ context.Context, key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.values[key] == value {
		delete(c.values, key)
	}
	return nil
}

type cryptoSecurityTestRepo struct {
	UserRepository
	mu     sync.Mutex
	user   User
	writes int
}

func (r *cryptoSecurityTestRepo) GetByID(context.Context, int64) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.user
	return &u, nil
}

func (r *cryptoSecurityTestRepo) UpdateTotpSecret(_ context.Context, _ int64, encrypted *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.user.TotpSecretEncrypted = encrypted
	r.writes++
	return nil
}

func (r *cryptoSecurityTestRepo) EnableTotp(context.Context, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.user.TotpEnabled = true
	r.writes++
	return nil
}

func (r *cryptoSecurityTestRepo) BindTotpIfDisabled(_ context.Context, _ int64, encrypted string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user.TotpEnabled || !r.user.IsActive() || r.user.DeletedAt != nil {
		return ErrUserSecurityStateChanged
	}
	r.user.TotpSecretEncrypted = &encrypted
	r.user.TotpEnabled = true
	r.writes++
	return nil
}

func (r *cryptoSecurityTestRepo) DisableTotpWithExpectedRole(_ context.Context, _ int64, role string, secret *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user.Role != role || !r.user.IsActive() || r.user.DeletedAt != nil ||
		(r.user.TotpSecretEncrypted == nil) != (secret == nil) || (secret != nil && *r.user.TotpSecretEncrypted != *secret) {
		return ErrUserSecurityStateChanged
	}
	r.user.TotpEnabled = false
	r.user.TotpSecretEncrypted = nil
	r.writes++
	return nil
}

func (r *cryptoSecurityTestRepo) DisableTotp(context.Context, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.user.TotpEnabled = false
	r.user.TotpSecretEncrypted = nil
	r.writes++
	return nil
}

type cryptoSecurityTestEncryptor struct{}

func (cryptoSecurityTestEncryptor) Encrypt(v string) (string, error) { return "encrypted:" + v, nil }
func (cryptoSecurityTestEncryptor) Decrypt(v string) (string, error) {
	if !strings.HasPrefix(v, "encrypted:") {
		return "", errors.New("invalid ciphertext")
	}
	return strings.TrimPrefix(v, "encrypted:"), nil
}

func newCryptoSecurityTestServices(t *testing.T, enabled bool) (*CryptoSecurityService, *TotpService, *cryptoSecurityTestRepo, *cryptoSecurityTestCache) {
	return newCryptoSecurityTestServicesWithOperators(t, enabled, "7")
}

func newCryptoSecurityTestServicesWithOperators(t *testing.T, enabled bool, operators string) (*CryptoSecurityService, *TotpService, *cryptoSecurityTestRepo, *cryptoSecurityTestCache) {
	t.Helper()
	t.Setenv("CRYPTO_ADMIN_USER_IDS", operators)
	secret := "encrypted:" + cryptoSecurityTestSecret
	repo := &cryptoSecurityTestRepo{user: User{
		ID: 7, Email: "admin@example.test", Role: RoleAdmin, Status: StatusActive,
		TotpEnabled: enabled, TotpSecretEncrypted: &secret,
	}}
	require.NoError(t, repo.user.SetPassword("setup-password"))
	cache := &cryptoSecurityTestCache{values: map[string]string{}, counts: map[string]int{}}
	settings := NewSettingService(&totpVMSettingRepoStub{values: map[string]string{SettingKeyTotpEnabled: "true"}}, nil)
	totpService := NewTotpService(repo, cryptoSecurityTestEncryptor{}, cache, settings, nil, nil)
	cfg := &config.Config{}
	cfg.Totp.EncryptionKeyConfigured = true
	cfg.Totp.EncryptionKey = strings.Repeat("ab", 32)
	return NewCryptoSecurityService(totpService, cfg), totpService, repo, cache
}

func TestCryptoSecurityRequiresDeploymentPinnedPaymentAdministrator(t *testing.T) {
	for _, configured := range []string{"", "8", "7,invalid", "0,7", "7,", "+7", "07", "9223372036854775808", strings.Repeat("7,", 65) + "7"} {
		t.Run(configured, func(t *testing.T) {
			security, _, repo, cache := newCryptoSecurityTestServicesWithOperators(t, true, configured)
			_, err := security.Status(t.Context(), 7)
			require.ErrorIs(t, err, ErrCryptoOperatorRequired)
			require.ErrorIs(t, security.VerifyRisk(t.Context(), 7, cryptoSecurityTestCode(t, cryptoSecurityTestSecret)), ErrCryptoOperatorRequired)
			_, err = security.InitiateSetup(t.Context(), 7, "session")
			require.ErrorIs(t, err, ErrCryptoOperatorRequired)
			require.Zero(t, repo.writes)
			require.Empty(t, cache.values)
		})
	}
	security, _, repo, _ := newCryptoSecurityTestServicesWithOperators(t, true, " 7, 8,7 ")
	_, err := security.Status(t.Context(), 7)
	require.NoError(t, err)
	// Runtime changes and newly-created admin accounts cannot expand authority.
	t.Setenv("CRYPTO_ADMIN_USER_IDS", "9")
	_, err = security.Status(t.Context(), 7)
	require.NoError(t, err)
	repo.user.ID = 9
	_, err = security.CurrentOperator(t.Context(), 9)
	require.ErrorIs(t, err, ErrCryptoOperatorRequired)
}

func TestCryptoSecurityUnlistedAdminRetainsOrdinaryTotpEnrollmentAndDisable(t *testing.T) {
	security, svc, repo, _ := newCryptoSecurityTestServicesWithOperators(t, false, "8")
	setup, err := svc.InitiateSetup(t.Context(), 7, "", "setup-password")
	require.NoError(t, err)
	code := cryptoSecurityTestCode(t, setup.Secret)
	require.NoError(t, svc.CompleteSetup(t.Context(), 7, code, setup.SetupToken))
	require.True(t, repo.user.TotpEnabled)
	require.ErrorIs(t, security.VerifyRisk(t.Context(), 7, code), ErrCryptoOperatorRequired)

	_, svc, repo, _ = newCryptoSecurityTestServicesWithOperators(t, true, "8")
	require.NoError(t, svc.Disable(t.Context(), 7, "", "", cryptoSecurityTestCode(t, cryptoSecurityTestSecret)))
	require.False(t, repo.user.TotpEnabled)
}

func cryptoSecurityTestCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	return code
}

func TestCryptoSecurityGeneralEnrollmentCannotReplayCodeForPayment(t *testing.T) {
	security, svc, _, cache := newCryptoSecurityTestServices(t, false)
	setup, err := svc.InitiateSetup(context.Background(), 7, "", "setup-password")
	require.NoError(t, err)
	require.True(t, cache.setup.SecretEncrypted, "the general endpoint must not store a plaintext admin payment seed")
	require.NotEqual(t, setup.Secret, cache.setup.Secret)
	code := cryptoSecurityTestCode(t, setup.Secret)
	require.NoError(t, svc.CompleteSetup(context.Background(), 7, code, setup.SetupToken))
	require.ErrorIs(t, security.VerifyRisk(context.Background(), 7, code), ErrCryptoTotpReplayed)
}

func TestCryptoSecurityOrdinaryLoginTotpWithoutPaymentKey(t *testing.T) {
	security, svc, _, _ := newCryptoSecurityTestServices(t, false)
	security.key = nil
	ctx := context.Background()
	setup, err := svc.InitiateSetup(ctx, 7, "", "setup-password")
	require.NoError(t, err)
	code := cryptoSecurityTestCode(t, setup.Secret)
	require.NoError(t, svc.CompleteSetup(ctx, 7, code, setup.SetupToken))
	require.NoError(t, svc.VerifyCode(ctx, 7, code))
	require.ErrorIs(t, security.VerifyRisk(ctx, 7, code), ErrCryptoTotpUnavailable)
}

func TestCryptoSecurityEnrollmentNeedsNoPasswordAndCannotReplaceBoundSeed(t *testing.T) {
	security, svc, repo, cache := newCryptoSecurityTestServices(t, false)
	ctx := context.Background()
	repo.user.PasswordHash = "not-used-for-payment-enrollment"
	setup, err := security.InitiateSetup(ctx, 7, "session")
	require.NoError(t, err)
	require.NotNil(t, cache.setup)
	code := cryptoSecurityTestCode(t, setup.Secret)
	require.ErrorIs(t, security.Enable(ctx, 7, "other-session", code, setup.SetupToken), ErrCryptoTotpRequired)
	require.NoError(t, security.Enable(ctx, 7, "session", code, setup.SetupToken))
	repo.user.TotpEnabled = true
	_, err = security.InitiateSetup(ctx, 7, "session")
	require.ErrorIs(t, err, ErrTotpAlreadyEnabled)
	_, err = svc.InitiateSetup(ctx, 7, "", "setup-password")
	require.ErrorIs(t, err, ErrTotpAlreadyEnabled)
	require.Equal(t, 1, repo.writes, "seed and enabled state are committed atomically")
}

func TestCryptoSecurityPaymentEnrollmentBoundToSessionAndEndpoint(t *testing.T) {
	security, svc, repo, _ := newCryptoSecurityTestServices(t, false)
	ctx := context.Background()
	setup, err := security.InitiateSetup(ctx, 7, "session-a")
	require.NoError(t, err)
	code := cryptoSecurityTestCode(t, setup.Secret)
	require.ErrorIs(t, security.Enable(ctx, 7, "session-b", code, setup.SetupToken), ErrCryptoTotpRequired)
	require.ErrorIs(t, svc.CompleteSetup(ctx, 7, code, setup.SetupToken), ErrTotpSetupExpired)
	require.Zero(t, repo.writes)
	require.NoError(t, security.Enable(ctx, 7, "session-a", code, setup.SetupToken))
	require.ErrorIs(t, security.Enable(ctx, 7, "session-a", code, setup.SetupToken), ErrCryptoTotpReplayed)
	require.ErrorIs(t, security.VerifyRisk(ctx, 7, code), ErrCryptoTotpReplayed)
}

func TestCryptoSecurityGeneralAdminEnrollmentCannotCrossJwtSessions(t *testing.T) {
	_, svc, repo, _ := newCryptoSecurityTestServices(t, false)
	ctx := context.Background()
	setup, err := svc.InitiateSetupForSession(ctx, 7, "", "setup-password", "session-a")
	require.NoError(t, err)
	code := cryptoSecurityTestCode(t, setup.Secret)
	require.ErrorIs(t, svc.CompleteSetupForSession(ctx, 7, code, setup.SetupToken, "session-b"), ErrTotpSetupExpired)
	require.Zero(t, repo.writes)
	require.NoError(t, svc.CompleteSetupForSession(ctx, 7, code, setup.SetupToken, "session-a"))
	require.True(t, repo.user.TotpEnabled)
}

func TestCryptoSecurityGeneralEnrollmentSharesPaymentLock(t *testing.T) {
	for _, action := range []string{"setup", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			security, svc, repo, _ := newCryptoSecurityTestServices(t, action == "disable")
			ctx := context.Background()
			var setup *TotpSetupResponse
			if action == "enable" {
				var err error
				setup, err = svc.InitiateSetup(ctx, 7, "", "setup-password")
				require.NoError(t, err)
			}
			unlock, err := security.lockSetup(ctx, 7)
			require.NoError(t, err)
			defer unlock()
			switch action {
			case "setup":
				_, err = svc.InitiateSetup(ctx, 7, "", "setup-password")
			case "enable":
				err = svc.CompleteSetup(ctx, 7, cryptoSecurityTestCode(t, setup.Secret), setup.SetupToken)
			case "disable":
				err = svc.Disable(ctx, 7, "", "", cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
			}
			require.ErrorIs(t, err, ErrCryptoTotpReplayed)
			require.Zero(t, repo.writes, "another enrollment owns the shared lock")
		})
	}
}

func TestCryptoSecurityReplayedCodeCannotResetFailureBudget(t *testing.T) {
	security, _, _, cache := newCryptoSecurityTestServices(t, true)
	ctx := context.Background()
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)
	require.NoError(t, security.VerifyRisk(ctx, 7, code))
	for i := 0; i < maxTotpAttempts-1; i++ {
		require.ErrorIs(t, security.VerifyRisk(ctx, 7, "bad"), ErrCryptoTotpRequired)
	}
	require.ErrorIs(t, security.VerifyRisk(ctx, 7, code), ErrCryptoTotpReplayed)
	attempts, err := cache.GetVerifyAttempts(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, maxTotpAttempts, attempts)
}

func TestCryptoSecurityAdminLoginAndStepUpCodesCannotReplayForPayment(t *testing.T) {
	for _, first := range []string{"login", "payment"} {
		t.Run(first, func(t *testing.T) {
			security, svc, _, _ := newCryptoSecurityTestServices(t, true)
			ctx := context.Background()
			code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)
			if first == "login" {
				require.NoError(t, svc.VerifyCode(ctx, 7, code))
				require.ErrorIs(t, security.VerifyRisk(ctx, 7, code), ErrCryptoTotpReplayed)
			} else {
				require.NoError(t, security.VerifyRisk(ctx, 7, code))
				require.ErrorIs(t, svc.VerifyCode(ctx, 7, code), ErrCryptoTotpReplayed)
			}
		})
	}
}

func TestCryptoSecurityRiskIsAtomicAcrossInstances(t *testing.T) {
	security, svc, repo, cache := newCryptoSecurityTestServices(t, true)
	otherTotp := NewTotpService(repo, svc.encryptor, cache, svc.settingService, nil, nil)
	other := NewCryptoSecurityService(otherTotp, &config.Config{Totp: config.TotpConfig{
		EncryptionKeyConfigured: true, EncryptionKey: strings.Repeat("ab", 32),
	}})
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)
	results := make(chan error, 2)
	for _, instance := range []*CryptoSecurityService{security, other} {
		go func(s *CryptoSecurityService) { results <- s.VerifyRisk(context.Background(), 7, code) }(instance)
	}
	var success, replay int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrCryptoTotpReplayed):
			replay++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, replay)
}

func TestCryptoSecurityCounterStableAcrossTimeWindow(t *testing.T) {
	security, _, _, _ := newCryptoSecurityTestServices(t, true)
	instant := time.Unix(1800000010, 0)
	code, err := totp.GenerateCode(cryptoSecurityTestSecret, instant)
	require.NoError(t, err)
	first, valid := validateTotpCode(code, cryptoSecurityTestSecret, instant)
	require.True(t, valid)
	second, valid := validateTotpCode(code, cryptoSecurityTestSecret, instant.Add(30*time.Second))
	require.True(t, valid)
	require.Equal(t, first, second)
	require.NoError(t, security.consume(context.Background(), 7, first, code))
	require.ErrorIs(t, security.consume(context.Background(), 7, second, code), ErrCryptoTotpReplayed)
}

func TestCryptoSecurityUsesLiveRoleAndFailsClosed(t *testing.T) {
	for _, change := range []string{"demoted", "disabled", "deleted", "unbound", "store-down", "global-switch-off"} {
		t.Run(change, func(t *testing.T) {
			security, svc, repo, cache := newCryptoSecurityTestServices(t, true)
			switch change {
			case "demoted":
				repo.user.Role = RoleUser
			case "disabled":
				repo.user.Status = "disabled"
			case "deleted":
				now := time.Now()
				repo.user.DeletedAt = &now
			case "unbound":
				repo.user.TotpEnabled = false
			case "store-down":
				cache.fail = true
			case "global-switch-off":
				svc.settingService = NewSettingService(&totpVMSettingRepoStub{values: map[string]string{SettingKeyTotpEnabled: "false"}}, nil)
			}
			err := security.VerifyRisk(context.Background(), 7, cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
			if change == "global-switch-off" {
				require.NoError(t, err)
			} else {
				require.Error(t, err, fmt.Sprintf("%s must not authorize payment", change))
			}
		})
	}
}
