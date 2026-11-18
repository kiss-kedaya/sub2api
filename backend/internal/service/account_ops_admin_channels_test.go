package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type opsAdminChannelsStub struct {
	user       *User
	configured bool
	enabled    bool
	key        string
	keys       []string
	err        error
}

func (a *opsAdminChannelsStub) PrimaryAdmin(context.Context) (*User, error) { return a.user, nil }
func (a *opsAdminChannelsStub) BarkStatus(context.Context) (bool, bool, error) {
	return a.configured, a.enabled, a.err
}
func (a *opsAdminChannelsStub) PushToAdmins(context.Context, string, string, string) error {
	if !a.configured || !a.enabled || a.key == "" {
		return errors.New("not configured")
	}
	if a.err != nil {
		return a.err
	}
	a.keys = append(a.keys, a.key)
	return nil
}

func opsAdminFixture() *opsAdminChannelsStub {
	return &opsAdminChannelsStub{user: &User{ID: 1, Role: RoleAdmin, Status: StatusActive, Email: "owner@example.invalid"}, configured: true, enabled: true, key: "synthetic-one"}
}

func TestAccountOpsAdminDefaultsLegacyAndExplicitChoices(t *testing.T) {
	for _, tt := range []struct {
		name, raw   string
		email, bark bool
	}{
		{"empty", `{}`, true, true},
		{"custom email", `{"recipient":"custom@example.invalid"}`, false, true},
		{"custom robot", `{"encrypted_webhooks":[{"id":"custom","provider":"custom","enabled":false,"url_cipher":"opaque","revision":"unchanged"}]}`, true, false},
		{"both custom", `{"recipient":"custom@example.invalid","encrypted_webhooks":[{"id":"custom","provider":"custom","enabled":true,"url_cipher":"opaque","revision":"unchanged"}]}`, false, false},
		{"explicit off", `{"use_admin_email":false,"use_admin_bark":false}`, false, false},
		{"explicit on", `{"recipient":"custom@example.invalid","use_admin_email":true,"use_admin_bark":true}`, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewAccountOpsService(&accountOpsSettingsStub{raw: tt.raw}, &accountOpsRepoStub{}, nil)
			cfg, err := svc.GetConfig(context.Background())
			require.NoError(t, err)
			require.NotNil(t, cfg.UseAdminEmail)
			require.NotNil(t, cfg.UseAdminBark)
			require.Equal(t, tt.email, *cfg.UseAdminEmail)
			require.Equal(t, tt.bark, *cfg.UseAdminBark)
		})
	}
}

func TestAccountOpsAdminChannelsFollowProfileChangesAndRetryIndependently(t *testing.T) {
	settings, repo, mail := &accountOpsSettingsStub{}, &accountOpsRepoStub{}, &accountOpsSenderStub{}
	svc := NewAccountOpsService(settings, repo, mail)
	admin := opsAdminFixture()
	svc.adminChannels = admin
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	require.NoError(t, svc.SaveConfig(context.Background(), cfg))
	admin.err = errors.New("offline")
	e := &AccountOpsEvent{AccountID: 7, Kind: "balance_low", Attempts: 1}
	svc.deliverNotification(context.Background(), e)
	require.Equal(t, "failed", repo.state)
	require.Equal(t, "sent", e.Deliveries[accountOpsAdminEmailID].Status)
	require.Equal(t, "failed", e.Deliveries[accountOpsAdminBarkID].Status)
	require.Equal(t, "owner@example.invalid", mail.to)
	admin.user.Email, admin.key, admin.err = "changed@example.invalid", "synthetic-two", nil
	svc.deliverNotification(context.Background(), e)
	require.Equal(t, "sent", repo.state)
	require.Equal(t, 1, mail.calls, "a new email address must not re-send an already delivered event")
	require.Equal(t, []string{"synthetic-two"}, admin.keys)
	svc.deliverNotification(context.Background(), &AccountOpsEvent{AccountID: 8, Kind: "balance_low"})
	require.Equal(t, "changed@example.invalid", mail.to)
	admin.key = "synthetic-three"
	require.NoError(t, svc.TestAdminChannel(context.Background(), "bark"))
	require.Equal(t, "synthetic-three", admin.keys[len(admin.keys)-1])
	require.NoError(t, svc.TestAdminChannel(context.Background(), "email"))
	require.Equal(t, "changed@example.invalid", mail.to)
	require.NotContains(t, settings.raw, "example.invalid")
	require.NotContains(t, settings.raw, "synthetic")
}

func TestAccountOpsAdminChannelsUnavailableNeverSucceedOrFallback(t *testing.T) {
	for _, name := range []string{"missing", "observer", "disabled", "deleted", "wrong id", "no email", "invalid email", "no bark", "bark off", "transport failure"} {
		t.Run(name, func(t *testing.T) {
			admin := opsAdminFixture()
			now := time.Now()
			switch name {
			case "missing":
				admin.user = nil
			case "observer":
				admin.user.Role = RoleUser
			case "disabled":
				admin.user.Status = "disabled"
			case "deleted":
				admin.user.DeletedAt = &now
			case "wrong id":
				admin.user.ID = 2
			case "no email":
				admin.user.Email = ""
			case "invalid email":
				admin.user.Email = "Owner <owner@example.invalid>"
			case "no bark":
				admin.configured = false
			case "bark off":
				admin.enabled = false
			case "transport failure":
				admin.err = errors.New("offline")
			}
			repo, mail := &accountOpsRepoStub{}, &accountOpsSenderStub{}
			svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, mail)
			svc.adminChannels = admin
			cfg := defaultAccountOpsConfig()
			cfg.Enabled = true
			require.NoError(t, svc.SaveConfig(context.Background(), cfg))
			e := &AccountOpsEvent{AccountID: 7, Kind: "balance_low"}
			svc.deliverNotification(context.Background(), e)
			require.Equal(t, "failed", repo.state)
			channel, id := "bark", accountOpsAdminBarkID
			if name == "no email" || name == "invalid email" {
				channel, id = "email", accountOpsAdminEmailID
			}
			require.Equal(t, "failed", e.Deliveries[id].Status)
			require.Error(t, svc.TestAdminChannel(context.Background(), channel))
			if name != "transport failure" {
				display := svc.AdminDefaults(context.Background())
				if channel == "email" {
					require.False(t, display.EmailAvailable)
				} else {
					require.False(t, display.BarkAvailable)
				}
			}
		})
	}
}

func TestAccountOpsAdminSwitchesPreserveCiphertextAndExplicitOff(t *testing.T) {
	ctx := context.Background()
	settings := &accountOpsSettingsStub{raw: `{"cooldown_minutes":60,"recipient":"custom@example.invalid","encrypted_webhooks":[{"id":"custom","provider":"dingtalk","enabled":true,"url_cipher":"opaque","secret_cipher":"opaque-secret","revision":"keep"}]}`}
	repo, mail := &accountOpsRepoStub{}, &accountOpsSenderStub{}
	svc := NewAccountOpsService(settings, repo, mail)
	svc.adminChannels = opsAdminFixture()
	on, off := true, false
	_, err := svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{UseAdminEmail: &on, UseAdminBark: &on})
	require.NoError(t, err)
	cooldown := 30
	_, err = svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{CooldownMinutes: &cooldown})
	require.NoError(t, err)
	require.Contains(t, settings.raw, `"use_admin_email":true`)
	require.Contains(t, settings.raw, `"use_admin_bark":true`)
	require.Contains(t, settings.raw, `"url_cipher":"opaque"`)
	require.Contains(t, settings.raw, `"secret_cipher":"opaque-secret"`)
	require.Contains(t, settings.raw, `"revision":"keep"`)
	_, err = svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{UseAdminEmail: &off, UseAdminBark: &off})
	require.NoError(t, err)
	c, err := svc.GetConfig(ctx)
	require.NoError(t, err)
	// An older full-config client omits the new fields; the persisted choice wins.
	c.UseAdminEmail, c.UseAdminBark = nil, nil
	require.NoError(t, svc.SaveConfig(ctx, c))
	c, err = svc.GetConfig(ctx)
	require.NoError(t, err)
	require.False(t, *c.UseAdminEmail)
	require.False(t, *c.UseAdminBark)
	raw, err := json.Marshal(svc.AdminDefaults(ctx))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "owner@example.invalid")
	require.NotContains(t, string(raw), "synthetic")
	c.Enabled, c.Webhooks = true, nil
	require.NoError(t, svc.SaveConfig(ctx, c))
	svc.deliverNotification(ctx, &AccountOpsEvent{AccountID: 7, Kind: "balance_low"})
	require.Equal(t, "custom@example.invalid", mail.to)
	admin, ok := svc.adminChannels.(*opsAdminChannelsStub)
	require.True(t, ok)
	require.Empty(t, admin.keys)
	empty := ""
	_, err = svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{Enabled: &off, Recipient: &empty})
	require.NoError(t, err)
	c, err = svc.GetConfig(ctx)
	require.NoError(t, err)
	require.False(t, *c.UseAdminEmail)
	require.False(t, *c.UseAdminBark)
}

func TestAccountOpsAdminEmailRetryUsesCurrentAddressWithoutRepeatingBark(t *testing.T) {
	mail := &accountOpsSenderStub{err: errors.New("smtp unavailable")}
	repo := &accountOpsRepoStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, mail)
	admin := opsAdminFixture()
	svc.adminChannels = admin
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	require.NoError(t, svc.SaveConfig(context.Background(), cfg))
	e := &AccountOpsEvent{AccountID: 7, Kind: "balance_low"}
	svc.deliverNotification(context.Background(), e)
	require.Equal(t, "failed", e.Deliveries[accountOpsAdminEmailID].Status)
	require.Equal(t, "sent", e.Deliveries[accountOpsAdminBarkID].Status)
	admin.user.Email, admin.key, mail.err = "new@example.invalid", "synthetic-two", nil
	svc.deliverNotification(context.Background(), e)
	require.Equal(t, "sent", repo.state)
	require.Equal(t, "new@example.invalid", mail.to)
	require.Equal(t, []string{"synthetic-one"}, admin.keys)
}

func TestAccountOpsAdminBarkRechecksSwitchAfterRateWait(t *testing.T) {
	ctx := context.Background()
	repo := &opsReservationStub{}
	svc := NewAccountOpsService(&accountOpsSettingsStub{}, repo, &accountOpsSenderStub{})
	admin := opsAdminFixture()
	svc.adminChannels = admin
	cfg := defaultAccountOpsConfig()
	cfg.Enabled = true
	require.NoError(t, svc.SaveConfig(ctx, cfg))
	repo.duringWait = func() {
		_, err := svc.SaveNotificationSettings(ctx, AccountOpsNotificationSettings{UseAdminBark: opsBool(false)})
		require.NoError(t, err)
	}
	svc.deliverNotification(ctx, &AccountOpsEvent{AccountID: 7, Kind: "balance_low"})
	require.Empty(t, admin.keys)
}

func TestAccountOpsAdminBarkCountsTowardsRobotLimit(t *testing.T) {
	cfg := defaultAccountOpsConfig()
	on := true
	cfg.UseAdminBark = &on
	for i := 0; i < 5; i++ {
		cfg.Webhooks = append(cfg.Webhooks, AccountOpsWebhook{ID: string(rune('a' + i)), Provider: "custom"})
	}
	require.Error(t, ValidateAccountOpsConfig(cfg))
	cfg.Webhooks = cfg.Webhooks[:4]
	require.NoError(t, ValidateAccountOpsConfig(cfg))
}
