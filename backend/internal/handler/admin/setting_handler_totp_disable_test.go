package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type globalTotpActorRepo struct {
	service.UserRepository
	actor *service.User
}

func (r *globalTotpActorRepo) GetByID(context.Context, int64) (*service.User, error) {
	user := *r.actor
	return &user, nil
}

func (r *globalTotpActorRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type globalTotpGrantKey struct {
	userID     int64
	sessionKey string
}

// Cache storage and expiry are exercised by the integration tests. This stub
// keeps these handler tests focused on which settings transitions require a grant.
type globalTotpGrantCache struct {
	service.TotpCache
	grants map[globalTotpGrantKey]bool
	err    error
	checks int
}

func (c *globalTotpGrantCache) SetStepUpGrant(_ context.Context, userID int64, sessionKey string, _ time.Duration) error {
	c.grants[globalTotpGrantKey{userID: userID, sessionKey: sessionKey}] = true
	return nil
}

func (c *globalTotpGrantCache) HasStepUpGrant(_ context.Context, userID int64, sessionKey string) (bool, error) {
	c.checks++
	return c.grants[globalTotpGrantKey{userID: userID, sessionKey: sessionKey}], c.err
}

func newGlobalTotpTestHandler(t *testing.T, totpEnabled, stepUpEnabled, actorTotpEnabled bool) (*SettingHandler, *settingHandlerRepoStub, *globalTotpGrantCache) {
	t.Helper()
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeyTotpEnabled:   strconv.FormatBool(totpEnabled),
		service.SettingKeyStepUpEnabled: strconv.FormatBool(stepUpEnabled),
	})
	actorRepo := &globalTotpActorRepo{actor: &service.User{
		ID: 42, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: actorTotpEnabled,
	}}
	cache := &globalTotpGrantCache{grants: make(map[globalTotpGrantKey]bool)}
	h.SetStepUpDeps(
		service.NewTotpService(actorRepo, nil, cache, h.settingService, nil, nil),
		service.NewUserService(actorRepo, repo, nil, nil),
	)
	return h, repo, cache
}

func prepareGlobalTotpActor(c *gin.Context) {
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Set(middleware.ContextKeySessionID, "settings-session")
	c.Set("auth_method", service.AuditAuthMethodJWT)
}

func requireGlobalTotpResponse(t *testing.T, body []byte, totpEnabled, stepUpEnabled bool) {
	t.Helper()
	var decoded struct {
		Data struct {
			TotpEnabled   bool `json:"totp_enabled"`
			StepUpEnabled bool `json:"step_up_enabled"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Equal(t, totpEnabled, decoded.Data.TotpEnabled)
	require.Equal(t, stepUpEnabled, decoded.Data.StepUpEnabled)
}

func TestUpdateSettingsGlobalTotpDisableRequiresCurrentActorGrant(t *testing.T) {
	for _, transition := range []struct {
		name            string
		totpEnabled     bool
		stepUpEnabled   bool
		body            map[string]any
		wantTotpEnabled bool
		wantStepUp      bool
	}{
		{name: "disable_totp_while_stepup_off", totpEnabled: true, body: map[string]any{"totp_enabled": false}},
		{name: "disable_totp_while_stepup_on", totpEnabled: true, stepUpEnabled: true, body: map[string]any{"totp_enabled": false}, wantStepUp: true},
		{name: "disable_stepup_keeps_omitted_totp", totpEnabled: true, stepUpEnabled: true, body: map[string]any{"step_up_enabled": false}, wantTotpEnabled: true},
		{name: "disable_stepup_while_global_totp_off", stepUpEnabled: true, body: map[string]any{"step_up_enabled": false}},
		{name: "disable_both", totpEnabled: true, stepUpEnabled: true, body: map[string]any{"totp_enabled": false, "step_up_enabled": false}},
	} {
		t.Run(transition.name, func(t *testing.T) {
			for _, actor := range []struct {
				name          string
				authenticated bool
				totpEnabled   bool
				adminAPIKey   bool
				grantUserID   int64
				grantSession  string
				cacheError    bool
				status        int
				reason        string
			}{
				{name: "no_authenticated_session", status: http.StatusUnauthorized, reason: "UNAUTHORIZED"},
				{name: "admin_api_key_even_with_grant", authenticated: true, totpEnabled: true, adminAPIKey: true, grantUserID: 42, grantSession: "settings-session", status: http.StatusForbidden, reason: "STEP_UP_ADMIN_API_KEY_FORBIDDEN"},
				{name: "actor_without_totp_even_with_grant", authenticated: true, grantUserID: 42, grantSession: "settings-session", status: http.StatusForbidden, reason: "STEP_UP_TOTP_NOT_ENABLED"},
				{name: "missing_grant", authenticated: true, totpEnabled: true, status: http.StatusForbidden, reason: "STEP_UP_REQUIRED"},
				{name: "another_users_grant", authenticated: true, totpEnabled: true, grantUserID: 43, grantSession: "settings-session", status: http.StatusForbidden, reason: "STEP_UP_REQUIRED"},
				{name: "another_sessions_grant", authenticated: true, totpEnabled: true, grantUserID: 42, grantSession: "other-session", status: http.StatusForbidden, reason: "STEP_UP_REQUIRED"},
				{name: "grant_cache_unavailable", authenticated: true, totpEnabled: true, cacheError: true, status: http.StatusServiceUnavailable, reason: "STEP_UP_UNAVAILABLE"},
				{name: "current_actor_current_session_grant", authenticated: true, totpEnabled: true, grantUserID: 42, grantSession: "settings-session", status: http.StatusOK},
			} {
				t.Run(actor.name, func(t *testing.T) {
					h, repo, cache := newGlobalTotpTestHandler(t, transition.totpEnabled, transition.stepUpEnabled, actor.totpEnabled)
					if actor.grantUserID != 0 {
						require.NoError(t, cache.SetStepUpGrant(context.Background(), actor.grantUserID, actor.grantSession, service.StepUpGrantTTL))
					}
					if actor.cacheError {
						cache.err = errors.New("step-up cache unavailable")
					}

					rec := doUpdateSettings(t, h, transition.body, func(c *gin.Context) {
						if actor.authenticated {
							prepareGlobalTotpActor(c)
						}
						if actor.adminAPIKey {
							c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
						}
					})

					require.Equal(t, actor.status, rec.Code, rec.Body.String())
					if actor.status != http.StatusOK {
						require.Contains(t, rec.Body.String(), actor.reason)
						require.Empty(t, repo.lastUpdates, "rejected requests must not save any settings")
						require.Equal(t, strconv.FormatBool(transition.totpEnabled), repo.values[service.SettingKeyTotpEnabled])
						require.Equal(t, strconv.FormatBool(transition.stepUpEnabled), repo.values[service.SettingKeyStepUpEnabled])
						return
					}

					require.Equal(t, strconv.FormatBool(transition.wantTotpEnabled), repo.values[service.SettingKeyTotpEnabled])
					require.Equal(t, strconv.FormatBool(transition.wantStepUp), repo.values[service.SettingKeyStepUpEnabled])
					for _, key := range []string{service.SettingKeyTotpEnabled, service.SettingKeyStepUpEnabled} {
						if _, changed := transition.body[key]; changed {
							require.Equal(t, "false", repo.lastUpdates[key])
						} else {
							require.NotContains(t, repo.lastUpdates, key, "omitted security switches must not be rewritten")
						}
					}
					requireGlobalTotpResponse(t, rec.Body.Bytes(), transition.wantTotpEnabled, transition.wantStepUp)
				})
			}
		})
	}
}

func TestUpdateSettingsGlobalTotpEnableDoesNotRequireStepUpGrant(t *testing.T) {
	for _, tc := range []struct {
		name string
		totp config.TotpConfig
	}{
		{name: "explicit_key", totp: config.TotpConfig{EncryptionKeyConfigured: true}},
		{name: "persistent_installation_key", totp: config.TotpConfig{EncryptionKeyPersistent: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo := newStepUpSwitchTestHandler(t, map[string]string{
				service.SettingKeyTotpEnabled:   "false",
				service.SettingKeyStepUpEnabled: "false",
			})
			h.settingService = service.NewSettingService(repo, &config.Config{
				Default: config.DefaultConfig{UserConcurrency: 5},
				Totp:    tc.totp,
			})

			// Enabling the feature needs a persistent key, but no recovery code or step-up grant.
			rec := doUpdateSettings(t, h, map[string]any{"totp_enabled": true}, prepareGlobalTotpActor)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "true", repo.lastUpdates[service.SettingKeyTotpEnabled])
			require.NotContains(t, repo.lastUpdates, service.SettingKeyStepUpEnabled)
			requireGlobalTotpResponse(t, rec.Body.Bytes(), true, false)
		})
	}
}

func TestUpdateSettingsGlobalTotpEnableRetainsEncryptionKeyPrecondition(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{service.SettingKeyTotpEnabled: "false"})
	h.settingService = service.NewSettingService(repo, &config.Config{
		Totp: config.TotpConfig{EncryptionKey: strings.Repeat("ab", 32)},
	})

	rec := doUpdateSettings(t, h, map[string]any{"totp_enabled": true}, prepareGlobalTotpActor)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "persistent installation encryption key is unavailable")
	require.Empty(t, repo.lastUpdates)
	require.Equal(t, "false", repo.values[service.SettingKeyTotpEnabled])
}

func TestUpdateSettingsGlobalTotpEnableStepUpNeedsBoundTotpWithoutGrant(t *testing.T) {
	for _, tc := range []struct {
		name        string
		actorTotp   bool
		adminAPIKey bool
		status      int
		reason      string
	}{
		{name: "bound_totp_without_grant", actorTotp: true, status: http.StatusOK},
		{name: "unbound_totp", status: http.StatusBadRequest, reason: "STEP_UP_ENABLE_REQUIRES_TOTP"},
		{name: "admin_api_key", actorTotp: true, adminAPIKey: true, status: http.StatusForbidden, reason: "STEP_UP_ADMIN_API_KEY_FORBIDDEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, repo, cache := newGlobalTotpTestHandler(t, true, false, tc.actorTotp)

			rec := doUpdateSettings(t, h, map[string]any{"step_up_enabled": true}, func(c *gin.Context) {
				prepareGlobalTotpActor(c)
				if tc.adminAPIKey {
					c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
				}
			})

			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Zero(t, cache.checks, "enabling step-up must not demand an existing step-up grant")
			if tc.status != http.StatusOK {
				require.Contains(t, rec.Body.String(), tc.reason)
				require.Empty(t, repo.lastUpdates)
				return
			}
			require.Equal(t, "true", repo.lastUpdates[service.SettingKeyStepUpEnabled])
			require.NotContains(t, repo.lastUpdates, service.SettingKeyTotpEnabled)
			requireGlobalTotpResponse(t, rec.Body.Bytes(), true, true)
		})
	}
}

func TestUpdateSettingsGlobalTotpUnchangedSwitchesAreNotWrittenOrGated(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run("stored_"+strconv.FormatBool(enabled)+"/explicit_"+strconv.FormatBool(explicit), func(t *testing.T) {
				h, repo := newStepUpSwitchTestHandler(t, map[string]string{
					service.SettingKeyTotpEnabled:   strconv.FormatBool(enabled),
					service.SettingKeyStepUpEnabled: strconv.FormatBool(enabled),
				})
				body := map[string]any{"registration_enabled": true}
				if explicit {
					body["totp_enabled"] = enabled
					body["step_up_enabled"] = enabled
				}

				// No actor/TOTP dependencies: an accidental gate would reject or panic.
				rec := doUpdateSettings(t, h, body, nil)

				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Equal(t, "true", repo.lastUpdates[service.SettingKeyRegistrationEnabled])
				require.NotContains(t, repo.lastUpdates, service.SettingKeyTotpEnabled)
				require.NotContains(t, repo.lastUpdates, service.SettingKeyStepUpEnabled)
				require.Equal(t, strconv.FormatBool(enabled), repo.values[service.SettingKeyTotpEnabled])
				require.Equal(t, strconv.FormatBool(enabled), repo.values[service.SettingKeyStepUpEnabled])
				requireGlobalTotpResponse(t, rec.Body.Bytes(), enabled, enabled)
			})
		}
	}
}
