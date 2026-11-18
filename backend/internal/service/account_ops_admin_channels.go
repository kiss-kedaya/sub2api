package service

import (
	"context"
	"errors"
	"time"
)

const (
	accountOpsAdminEmailID = "admin-email:1"
	accountOpsAdminBarkID  = "admin-bark:1"
)

// AccountOpsAdminChannels keeps profile credentials inside the existing strict
// Bark adapter instead of copying them into account notification settings.
type AccountOpsAdminChannels interface {
	PrimaryAdmin(context.Context) (*User, error)
	BarkStatus(context.Context) (bool, bool, error)
	PushToAdmins(context.Context, string, string, string) error
}

// AccountOpsAdminDefaults contains display-only status, never profile secrets.
type AccountOpsAdminDefaults struct {
	EmailAvailable         bool   `json:"email_available"`
	EmailMasked            string `json:"email_masked"`
	EmailUnavailableReason string `json:"email_unavailable_reason"`
	BarkConfigured         bool   `json:"bark_configured"`
	BarkEnabled            bool   `json:"bark_enabled"`
	BarkAvailable          bool   `json:"bark_available"`
	BarkUnavailableReason  string `json:"bark_unavailable_reason"`
}

func (c AccountOpsConfig) usesAdminEmail() bool {
	if c.UseAdminEmail != nil {
		return *c.UseAdminEmail
	}
	return c.Recipient == ""
}

func (c AccountOpsConfig) usesAdminBark() bool {
	if c.UseAdminBark != nil {
		return *c.UseAdminBark
	}
	return len(c.Webhooks) == 0
}

func (s *AccountOpsService) primaryAdmin(ctx context.Context) (*User, error) {
	if s.adminChannels == nil {
		return nil, errors.New("admin_unavailable")
	}
	u, err := s.adminChannels.PrimaryAdmin(ctx)
	if err != nil || u == nil || u.ID != 1 || !u.IsAdmin() || !u.IsActive() || u.DeletedAt != nil {
		return nil, errors.New("admin_unavailable")
	}
	return u, nil
}

func (s *AccountOpsService) adminEmail(ctx context.Context) (string, error) {
	u, err := s.primaryAdmin(ctx)
	if err != nil {
		return "", err
	}
	if u.Email == "" || validateAccountOpsRecipient(u.Email) != nil {
		return "", errors.New("email_unavailable")
	}
	return u.Email, nil
}

// AdminDefaults resolves the source on every read without persisting a snapshot.
func (s *AccountOpsService) AdminDefaults(ctx context.Context) AccountOpsAdminDefaults {
	out := AccountOpsAdminDefaults{}
	u, err := s.primaryAdmin(ctx)
	if err != nil {
		out.EmailUnavailableReason, out.BarkUnavailableReason = "admin_unavailable", "admin_unavailable"
		return out
	}
	if u.Email != "" && validateAccountOpsRecipient(u.Email) == nil {
		out.EmailAvailable, out.EmailMasked = true, MaskEmail(u.Email)
	} else {
		out.EmailUnavailableReason = "email_unavailable"
	}
	configured, enabled, err := s.adminChannels.BarkStatus(ctx)
	out.BarkConfigured, out.BarkEnabled = configured, enabled
	switch {
	case err != nil:
		out.BarkUnavailableReason = "bark_unavailable"
	case !configured:
		out.BarkUnavailableReason = "bark_unconfigured"
	case !enabled:
		out.BarkUnavailableReason = "bark_disabled"
	default:
		out.BarkAvailable = true
	}
	return out
}

func (s *AccountOpsService) sendAdminChannel(ctx context.Context, id string, message accountOpsMessage) error {
	switch id {
	case accountOpsAdminEmailID:
		recipient, err := s.adminEmail(ctx)
		if err != nil {
			return err
		}
		if s.email == nil {
			return errors.New("email_unavailable")
		}
		return s.email.SendEmail(ctx, recipient, message.title, message.html)
	case accountOpsAdminBarkID:
		if _, err := s.primaryAdmin(ctx); err != nil {
			return err
		}
		return s.adminChannels.PushToAdmins(ctx, message.robotTitle, message.plain, "account-ops")
	default:
		return errors.New("unknown admin channel")
	}
}

// TestAdminChannel exercises the same strict path as durable deliveries. It is
// independent of the master alert switch so setup can be checked before enabling.
func (s *AccountOpsService) TestAdminChannel(ctx context.Context, channel string) error {
	var id string
	switch channel {
	case "email":
		id = accountOpsAdminEmailID
	case "bark":
		id = accountOpsAdminBarkID
	default:
		return errors.New("unknown admin channel")
	}
	send, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if id == accountOpsAdminBarkID {
		if rate, ok := s.repo.(interface {
			ReserveRobotTest(context.Context, string) error
		}); ok {
			if err := rate.ReserveRobotTest(send, id); err != nil {
				return errors.New("robot test rate reservation failed")
			}
		}
	}
	message := s.compactRobotMessage(&AccountOpsEvent{AccountName: "测试账号", LastSeen: time.Now()}, "账号运维测试")
	message.html = message.plain
	return s.sendAdminChannel(send, id, message)
}
