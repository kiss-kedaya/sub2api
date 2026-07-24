package service

import (
	"context"
	"strings"
)

// Email alias normalization for registration dedup.
//
// Problem: registration duplicate checks compare the email verbatim (lowercase
// + trim only), so a single real inbox can spawn unlimited accounts using
// provider alias features:
//   - Plus addressing: user+tag@gmail.com is delivered to user@gmail.com
//     (supported by Gmail, Outlook/Hotmail, Yahoo, iCloud, Fastmail, and more).
//   - Gmail dot trick: u.s.e.r@gmail.com is delivered to user@gmail.com.
//
// This lets abusers bulk-register accounts (e.g. to farm signup grants) while
// the domain whitelist and email verification see each variant as a distinct,
// deliverable address. NormalizeEmailForAliasDedup collapses these variants to
// a single "inbox identity" so the registration path can reject duplicates.
//
// Normalization rules:
//   - All domains: lowercase, trim, and strip the local-part "+suffix".
//     Stripping the plus suffix on domains that do not support plus addressing
//     is harmless — those exact addresses are virtually never registered.
//   - Gmail family (gmail.com / googlemail.com): additionally remove dots from
//     the local part and fold the domain to gmail.com.
//
// This only affects registration duplicate detection. It intentionally does not
// change how emails are stored, displayed, or used for login/delivery.

var gmailFamilyDomains = map[string]struct{}{
	"gmail.com":      {},
	"googlemail.com": {},
}

// NormalizeEmailForAliasDedup returns the canonical "inbox identity" of an
// email. Malformed input is returned lowercased/trimmed unchanged; format
// validation is the caller's responsibility.
func NormalizeEmailForAliasDedup(email string) string {
	local, domain, ok := splitEmailForPolicy(email)
	if !ok {
		return strings.ToLower(strings.TrimSpace(email))
	}
	if idx := strings.IndexByte(local, '+'); idx >= 0 {
		local = local[:idx]
	}
	if _, isGmail := gmailFamilyDomains[domain]; isGmail {
		local = strings.ReplaceAll(local, ".", "")
		domain = "gmail.com"
	}
	return local + "@" + domain
}

// aliasDedupCandidateDomains returns the stored domains to scan when checking
// for an alias collision: gmail-family domains are mutual aliases, every other
// domain only collides with itself.
func aliasDedupCandidateDomains(email string) []string {
	_, domain, ok := splitEmailForPolicy(email)
	if !ok {
		return nil
	}
	if _, isGmail := gmailFamilyDomains[domain]; isGmail {
		return []string{"gmail.com", "googlemail.com"}
	}
	return []string{domain}
}

// emailAliasLookupRepo is an optional capability of UserRepository, declared at
// the point of use so the core interface (and its test doubles) stay untouched.
type emailAliasLookupRepo interface {
	ListEmailsByDomains(ctx context.Context, domains []string) ([]string, error)
}

// existsByEmailOrAlias reports whether an email — or any alias variant that
// resolves to the same inbox — is already registered.
//
// It first performs the exact ExistsByEmail check, then, only on a miss, scans
// the candidate domains for an alias collision. Consistent with ExistsByEmail,
// a lookup error is surfaced (fail-closed) so the registration path returns a
// service error rather than letting an attacker bypass the check by inducing
// errors. Repositories without the alias-lookup capability degrade to the exact
// check.
func (s *AuthService) existsByEmailOrAlias(ctx context.Context, email string) (bool, error) {
	exists, err := s.userRepo.ExistsByEmail(ctx, email)
	if err != nil || exists {
		return exists, err
	}

	repo, ok := s.userRepo.(emailAliasLookupRepo)
	if !ok {
		return false, nil
	}
	domains := aliasDedupCandidateDomains(email)
	if len(domains) == 0 {
		return false, nil
	}
	existing, err := repo.ListEmailsByDomains(ctx, domains)
	if err != nil {
		return false, err
	}
	normalized := NormalizeEmailForAliasDedup(email)
	for _, candidate := range existing {
		if NormalizeEmailForAliasDedup(candidate) == normalized {
			return true, nil
		}
	}
	return false, nil
}
