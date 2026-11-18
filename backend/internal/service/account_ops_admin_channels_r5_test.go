package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountOpsR5LegacyUpgradeDestinations(t *testing.T) {
	for _, tt := range []struct {
		name                        string
		email, robot, disabledRobot bool
		barkConfigured, barkEnabled bool
		adminEmail, adminBark       bool
		mailTo, state               string
		customCalls, adminBarkCalls int
	}{
		{name: "both custom", email: true, robot: true, barkConfigured: true, barkEnabled: true, mailTo: "custom@example.invalid", state: "sent", customCalls: 1},
		{name: "email only", email: true, barkConfigured: true, barkEnabled: true, mailTo: "custom@example.invalid", state: "sent"},
		{name: "email only without admin Bark", email: true, mailTo: "custom@example.invalid", state: "sent"},
		{name: "robot only", robot: true, barkConfigured: true, barkEnabled: true, state: "sent", customCalls: 1},
		{name: "disabled robot only", robot: true, disabledRobot: true, barkConfigured: true, barkEnabled: true, state: "suppressed"},
		{name: "no custom with admin Bark", barkConfigured: true, barkEnabled: true, adminEmail: true, adminBark: true, mailTo: "owner@example.invalid", state: "sent", adminBarkCalls: 1},
		{name: "no custom without admin Bark", adminEmail: true, mailTo: "owner@example.invalid", state: "sent"},
		{name: "no custom with disabled admin Bark", barkConfigured: true, adminEmail: true, mailTo: "owner@example.invalid", state: "sent"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			stored := storedOpsConfig{AccountOpsConfig: defaultAccountOpsConfig()}
			stored.Enabled = !tt.disabledRobot
			if tt.email {
				stored.Recipient = "custom@example.invalid"
			}
			if tt.robot {
				cipher, err := (opsTestEncryptor{}).Encrypt("https://api.day.app/synthetic-r5")
				require.NoError(t, err)
				stored.StoredWebhooks = []storedOpsWebhook{{ID: "custom", Provider: "bark", Enabled: !tt.disabledRobot, URLCipher: cipher, Revision: "legacy"}}
			}
			raw, err := json.Marshal(stored)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "use_admin_")
			settings := &accountOpsSettingsStub{raw: string(raw)}
			repo, mail, admin := &accountOpsRepoStub{}, &accountOpsSenderStub{}, opsAdminFixture()
			admin.configured, admin.enabled = tt.barkConfigured, tt.barkEnabled
			svc := NewAccountOpsService(settings, repo, mail)
			svc.adminChannels = admin
			svc.SetNotificationDependencies(nil, opsTestEncryptor{}, true, "Asia/Shanghai")
			customCalls := 0
			svc.robotClient = &http.Client{Transport: opsRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				customCalls++
				require.Equal(t, "/synthetic-r5", req.URL.Path)
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":200}`)), Header: http.Header{}}, nil
			})}
			cfg, err := svc.GetConfig(ctx)
			require.NoError(t, err)
			require.NotNil(t, cfg.UseAdminEmail)
			require.NotNil(t, cfg.UseAdminBark)
			require.Equal(t, tt.adminEmail, *cfg.UseAdminEmail)
			require.Equal(t, tt.adminBark, *cfg.UseAdminBark)
			require.Equal(t, tt.barkConfigured && tt.barkEnabled, svc.AdminDefaults(ctx).BarkAvailable)
			e := &AccountOpsEvent{AccountID: 7, Kind: "balance_low", Attempts: 1}
			svc.deliverNotification(ctx, e)
			require.Equal(t, tt.state, repo.state)
			require.Equal(t, tt.mailTo, mail.to)
			mailCalls := 0
			if tt.mailTo != "" {
				mailCalls = 1
			}
			require.Equal(t, mailCalls, mail.calls)
			require.Equal(t, tt.customCalls, customCalls)
			require.Len(t, admin.keys, tt.adminBarkCalls)
			require.Len(t, e.Deliveries, mailCalls+tt.customCalls+tt.adminBarkCalls)
			for _, receipt := range e.Deliveries {
				require.Equal(t, "sent", receipt.Status)
				require.Equal(t, 1, receipt.Attempts)
			}
			require.Equal(t, time.Hour, repo.delay)
			require.Equal(t, string(raw), settings.raw, "reading defaults must not migrate stored settings")
		})
	}
}

func TestAccountOpsR5SaveFreezesEffectiveReferences(t *testing.T) {
	for _, name := range []string{"delete last robot", "add first disabled robot", "legacy partial save", "legacy full save", "Bark enabled before save", "Bark disabled before save", "explicit switches win"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			raw := `{"enabled":true,"cooldown_minutes":60}`
			wantEmail, wantBark := true, true
			if name == "delete last robot" {
				raw = `{"enabled":true,"recipient":"custom@example.invalid","cooldown_minutes":60,"encrypted_webhooks":[{"id":"last","provider":"bark","enabled":true,"url_cipher":"opaque","revision":"legacy"}]}`
				wantEmail, wantBark = false, false
			}
			settings := &accountOpsSettingsStub{raw: raw}
			repo, mail, admin := &accountOpsRepoStub{}, &accountOpsSenderStub{}, opsAdminFixture()
			if name == "Bark enabled before save" {
				admin.configured, admin.enabled = false, false
			}
			svc := NewAccountOpsService(settings, repo, mail)
			svc.adminChannels = admin
			svc.SetNotificationDependencies(nil, opsTestEncryptor{}, true, "Asia/Shanghai")
			before, err := svc.GetConfig(ctx)
			require.NoError(t, err)
			var after AccountOpsConfig
			switch name {
			case "delete last robot":
				after, err = svc.DeleteWebhook(ctx, "last")
			case "add first disabled robot":
				after, err = svc.SaveWebhook(ctx, "first", AccountOpsWebhook{Provider: "bark", Enabled: false, URL: "https://api.day.app/synthetic-r5"})
			case "legacy full save":
				before.UseAdminEmail, before.UseAdminBark = nil, nil
				before.Recipient = "custom@example.invalid"
				err = svc.SaveConfig(ctx, before)
				if err == nil {
					after, err = svc.GetConfig(ctx)
				}
			default:
				cooldown := 30
				request := AccountOpsNotificationSettings{CooldownMinutes: &cooldown}
				if name == "Bark enabled before save" {
					admin.configured, admin.enabled = true, true
				}
				if name == "Bark disabled before save" {
					admin.enabled = false
					wantBark = false
				}
				if name == "explicit switches win" {
					request.UseAdminEmail, request.UseAdminBark = opsBool(false), opsBool(true)
					wantEmail = false
				}
				after, err = svc.SaveNotificationSettings(ctx, request)
			}
			require.NoError(t, err)
			require.NotNil(t, after.UseAdminEmail)
			require.NotNil(t, after.UseAdminBark)
			require.Equal(t, wantEmail, *after.UseAdminEmail)
			require.Equal(t, wantBark, *after.UseAdminBark)
			var persisted storedOpsConfig
			require.NoError(t, json.Unmarshal([]byte(settings.raw), &persisted))
			require.NotNil(t, persisted.UseAdminEmail)
			require.NotNil(t, persisted.UseAdminBark)
			require.Equal(t, wantEmail, *persisted.UseAdminEmail)
			require.Equal(t, wantBark, *persisted.UseAdminBark)
			admin.enabled = true
			svc.deliverNotification(ctx, &AccountOpsEvent{AccountID: 7, Kind: "balance_low", Attempts: 1})
			require.Equal(t, "sent", repo.state)
			require.Equal(t, wantBark, len(admin.keys) == 1)
			if wantEmail {
				require.Equal(t, "owner@example.invalid", mail.to)
			} else if name == "delete last robot" {
				require.Equal(t, "custom@example.invalid", mail.to)
			} else {
				require.Zero(t, mail.calls)
			}
		})
	}
}

type opsR5ConfigSwitch struct {
	SettingRepository
	reads, switchAfter int
	before, after      string
}

func (s *opsR5ConfigSwitch) GetValue(context.Context, string) (string, error) {
	s.reads++
	if s.reads > s.switchAfter {
		return s.after, nil
	}
	return s.before, nil
}

func TestAccountOpsR5EmailSourceChangeRetriesWithoutRepeatingSuccess(t *testing.T) {
	for _, before := range []bool{true, false} {
		for _, bark := range []bool{false, true} {
			for _, switchAfter := range []int{1, 2} {
				t.Run(fmt.Sprintf("admin=%t/bark=%t/read=%d", before, bark, switchAfter), func(t *testing.T) {
					settings := &opsR5ConfigSwitch{
						switchAfter: switchAfter,
						before:      fmt.Sprintf(`{"enabled":true,"recipient":"custom@example.invalid","cooldown_minutes":60,"use_admin_email":%t,"use_admin_bark":%t}`, before, bark),
						after:       fmt.Sprintf(`{"enabled":true,"recipient":"custom@example.invalid","cooldown_minutes":60,"use_admin_email":%t,"use_admin_bark":%t}`, !before, bark),
					}
					repo, mail, admin := &accountOpsRepoStub{}, &accountOpsSenderStub{}, opsAdminFixture()
					svc := NewAccountOpsService(settings, repo, mail)
					svc.adminChannels = admin
					e := &AccountOpsEvent{AccountID: 7, Kind: "balance_low", Attempts: 1}
					svc.deliverNotification(context.Background(), e)
					require.Equal(t, "failed", repo.state, "a stale destination list cannot acknowledge an unsent email")
					require.Equal(t, 5*time.Minute, repo.delay)
					require.Zero(t, mail.calls)
					if bark {
						require.Equal(t, "sent", e.Deliveries[accountOpsAdminBarkID].Status)
					} else {
						require.Empty(t, e.Deliveries)
					}
					e.Attempts++
					svc.deliverNotification(context.Background(), e)
					require.Equal(t, "sent", repo.state)
					require.Equal(t, 1, mail.calls)
					wantTo, wantID := "owner@example.invalid", accountOpsAdminEmailID
					if before {
						wantTo, wantID = "custom@example.invalid", opsEmailIdentity("custom@example.invalid")
					}
					require.Equal(t, wantTo, mail.to)
					require.Equal(t, "sent", e.Deliveries[wantID].Status)
					require.Equal(t, 1, e.Deliveries[wantID].Attempts)
					svc.deliverNotification(context.Background(), e)
					require.Equal(t, 1, mail.calls)
					barkCalls := 0
					if bark {
						barkCalls = 1
					}
					require.Len(t, admin.keys, barkCalls)
				})
			}
		}
	}
}

func TestAccountOpsR5ExplicitUnavailableBarkStillFails(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			repo, mail, admin := &accountOpsRepoStub{}, &accountOpsSenderStub{}, opsAdminFixture()
			admin.configured, admin.enabled = configured, false
			svc := NewAccountOpsService(&accountOpsSettingsStub{raw: `{"enabled":true,"cooldown_minutes":60,"use_admin_email":true,"use_admin_bark":true}`}, repo, mail)
			svc.adminChannels = admin
			e := &AccountOpsEvent{AccountID: 7, Kind: "balance_low", Attempts: 1}
			svc.deliverNotification(context.Background(), e)
			require.Equal(t, "failed", repo.state)
			require.Equal(t, "sent", e.Deliveries[accountOpsAdminEmailID].Status)
			require.Equal(t, "failed", e.Deliveries[accountOpsAdminBarkID].Status)
			require.Equal(t, 5*time.Minute, repo.delay)
			require.Error(t, svc.TestAdminChannel(context.Background(), "bark"))
			e.Attempts++
			svc.deliverNotification(context.Background(), e)
			require.Equal(t, 1, mail.calls)
			require.Equal(t, 2, e.Deliveries[accountOpsAdminBarkID].Attempts)
		})
	}
}
