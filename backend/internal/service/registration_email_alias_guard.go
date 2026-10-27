// Alias syntax classification adapted from ranxi2001/sub2api @ 9620d315,
// LGPL-3.0-or-later. Deduplication remains independently enabled.
package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyRegistrationEmailAliasBlockEnabled = "registration_email_alias_block_enabled"

var ErrEmailAliasNotAllowed = infraerrors.BadRequest("EMAIL_ALIAS_NOT_ALLOWED", "Email aliases are not allowed for registration")

// Non-Gmail local dots can identify distinct inboxes and must remain accepted.
func IsRegistrationEmailAlias(email string) bool {
	local, domain, ok := splitEmailForAliasDedup(email)
	if !ok {
		return false
	}
	return strings.HasSuffix(strings.TrimSpace(email), ".") || strings.IndexByte(local, '+') > 0 ||
		(isGmailFamilyDomain(domain) && strings.Contains(local, "."))
}

func (s *SettingService) IsRegistrationEmailAliasBlockEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return false
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailAliasBlockEnabled)
	return err == nil && value == "true"
}

func (s *AuthService) validateRegistrationEmailAlias(ctx context.Context, email string) error {
	if !IsRegistrationEmailAlias(email) || s == nil || s.settingService == nil {
		return nil
	}
	// A database failure cannot bypass an enabled policy. Read explicitly here
	// instead of using the public display helper's safe disabled default.
	value, err := s.settingService.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailAliasBlockEnabled)
	if err != nil && err != ErrSettingNotFound {
		return ErrServiceUnavailable
	}
	if value == "true" {
		return ErrEmailAliasNotAllowed
	}
	return nil
}
