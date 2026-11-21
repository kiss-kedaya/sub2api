package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Key-rotation gate (security incident response).
//
// Every API key may have leaked. Until a user has either rotated all active API
// keys (created or rotated at/after security.key_rotation_cutoff) or put every active key
// behind a non-empty IP allowlist, they may not redeem codes or top up (payment
// orders, crypto deposits). This gate is deliberately narrow: model calls,
// login, balance reads and key management (create/delete/update IP allowlist)
// stay reachable so the user can actually perform the remediation.
const (
	// KeyRotationRequiredCode is the machine-readable error code attached to
	// every blocked request (403).
	KeyRotationRequiredCode = "KEY_ROTATION_REQUIRED"
	// KeyRotationRequiredMessage is the user-facing explanation.
	KeyRotationRequiredMessage = "为保护您的账户安全，请先更换 API Key 或为其设置 IP 白名单后再充值/兑换"
	// KeyRotationRemediationPath is the console route that lets the user fix it.
	KeyRotationRemediationPath = "/keys"

	// KeyRotationReason* are the machine-readable explanations of an evaluation.
	KeyRotationReasonDisabled         = "gate_disabled"
	KeyRotationReasonNoActiveKeys     = "no_active_keys"
	KeyRotationReasonAllKeysRotated   = "all_keys_rotated"
	KeyRotationReasonAllKeysAllowlist = "all_keys_ip_allowlisted"
	KeyRotationReasonRotationRequired = "rotation_required"
	KeyRotationReasonCheckUnavailable = "check_unavailable"
)

// ErrKeyRotationRequired is the 403 returned by every gated endpoint.
var ErrKeyRotationRequired = infraerrors.Forbidden(KeyRotationRequiredCode, KeyRotationRequiredMessage)

// KeyRotationActiveKeyLister lists a user's active ("启用中") API keys without
// pagination. *APIKeyService implements it.
type KeyRotationActiveKeyLister interface {
	ListActiveKeysByUser(ctx context.Context, userID int64) ([]APIKey, error)
}

// KeyRotationGateStatus is the evaluation result, exposed to the console so the
// recharge / redeem / deposit pages can explain the requirement before the user
// clicks anything.
type KeyRotationGateStatus struct {
	// Enabled mirrors security.key_rotation_gate_enabled.
	Enabled bool `json:"enabled"`
	// Required is true when the user must remediate before redeeming or topping up.
	Required bool `json:"required"`
	// Cutoff is security.key_rotation_cutoff (UTC).
	Cutoff time.Time `json:"cutoff"`
	// ActiveKeyCount / CompliantKeyCount / PendingKeyCount describe the user's
	// active keys. Compliant = created at/after cutoff OR has an IP allowlist.
	ActiveKeyCount    int `json:"active_key_count"`
	CompliantKeyCount int `json:"compliant_key_count"`
	PendingKeyCount   int `json:"pending_key_count"`
	// Reason is one of the KeyRotationReason* values.
	Reason string `json:"reason"`
	// ErrorCode / Message are only set while Required is true.
	ErrorCode string `json:"error_code,omitempty"`
	Message   string `json:"message,omitempty"`
	// RemediationPath is the console route that lets the user rotate keys.
	RemediationPath string `json:"remediation_path"`
}

// IsKeyRotationCompliant reports whether the current credential was issued
// at or after the cutoff, or the key carries a non-empty IP allowlist.
func IsKeyRotationCompliant(key APIKey, cutoff time.Time) bool {
	if hasNonEmptyIPWhitelist(key.IPWhitelist) {
		return true
	}
	return !keyRotationIssuedAt(key).Before(cutoff)
}

// keyRotationIssuedAt is when the currently valid credential was issued: the
// later of creation and the last rotation. A metadata edit does not move it.
func keyRotationIssuedAt(key APIKey) time.Time {
	issuedAt := key.CreatedAt
	if key.KeyRotatedAt != nil && key.KeyRotatedAt.After(issuedAt) {
		issuedAt = *key.KeyRotatedAt
	}
	return issuedAt
}

// KeyRotationKeyReason* classify why one active key still needs remediation.
// A key the gate blocks always satisfies both: it predates the cutoff and has
// no usable IP allowlist.
const (
	KeyRotationKeyReasonCreatedBeforeCutoff = "created_before_cutoff"
	KeyRotationKeyReasonNoIPAllowlist       = "no_ip_allowlist"
)

// KeyRotationKeyAssessment is the single-key verdict the console uses to badge
// rows on /keys and to count how many keys still need work.
type KeyRotationKeyAssessment struct {
	NeedsRotation bool
	Reasons       []string
}

// AssessKeyRotation decides whether one key is a credential the recharge gate
// is still waiting on. It deliberately reuses IsKeyRotationCompliant instead of
// re-implementing the predicate, and only active keys participate because the
// gate lists active keys only (a disabled key cannot be abused through the
// gateway and must never be badged as pending).
func AssessKeyRotation(key APIKey, enabled bool, cutoff time.Time) KeyRotationKeyAssessment {
	if !enabled || key.Status != StatusAPIKeyActive {
		return KeyRotationKeyAssessment{}
	}
	if IsKeyRotationCompliant(key, cutoff) {
		return KeyRotationKeyAssessment{}
	}
	reasons := make([]string, 0, 2)
	if keyRotationIssuedAt(key).Before(cutoff) {
		reasons = append(reasons, KeyRotationKeyReasonCreatedBeforeCutoff)
	}
	if !hasNonEmptyIPWhitelist(key.IPWhitelist) {
		reasons = append(reasons, KeyRotationKeyReasonNoIPAllowlist)
	}
	return KeyRotationKeyAssessment{NeedsRotation: true, Reasons: reasons}
}

func hasNonEmptyIPWhitelist(entries []string) bool {
	for _, entry := range entries {
		if strings.TrimSpace(entry) != "" {
			return true
		}
	}
	return false
}

// KeyRotationGateService evaluates whether a user may redeem codes / top up.
type KeyRotationGateService struct {
	keys KeyRotationActiveKeyLister
	cfg  *config.Config
}

// NewKeyRotationGateService builds the gate evaluator. *APIKeyService is bound to
// KeyRotationActiveKeyLister in the wire provider set; tests inject stubs.
func NewKeyRotationGateService(apiKeys KeyRotationActiveKeyLister, cfg *config.Config) *KeyRotationGateService {
	return &KeyRotationGateService{keys: apiKeys, cfg: cfg}
}

// Settings returns the effective switch and cutoff.
func (s *KeyRotationGateService) Settings() (enabled bool, cutoff time.Time) {
	if s == nil {
		return false, config.DefaultKeyRotationCutoffTime()
	}
	return s.cfg.KeyRotationGateSettings()
}

// Evaluate computes the gate status for one user. It returns an error only when
// the key listing itself failed; callers decide fail-open vs fail-closed.
func (s *KeyRotationGateService) Evaluate(ctx context.Context, userID int64) (*KeyRotationGateStatus, error) {
	enabled, cutoff := s.Settings()
	status := &KeyRotationGateStatus{
		Enabled:         enabled,
		Cutoff:          cutoff.UTC(),
		RemediationPath: KeyRotationRemediationPath,
	}
	if !enabled {
		status.Reason = KeyRotationReasonDisabled
		return status, nil
	}
	if s.keys == nil {
		// Misconfigured wiring must not lock every user out of recharge; report
		// the gate as disabled but keep the reason distinguishable.
		slog.Warn("key rotation gate has no API key lister; treating as disabled")
		status.Enabled = false
		status.Reason = KeyRotationReasonDisabled
		return status, nil
	}

	keys, err := s.keys.ListActiveKeysByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("key rotation gate: list active api keys for user %d: %w", userID, err)
	}
	status.ActiveKeyCount = len(keys)
	if len(keys) == 0 {
		// A user with no active key has nothing leaked that could be abused here.
		status.Reason = KeyRotationReasonNoActiveKeys
		return status, nil
	}

	compliant, allAllowlisted := 0, true
	for i := range keys {
		if IsKeyRotationCompliant(keys[i], cutoff) {
			compliant++
		}
		if !hasNonEmptyIPWhitelist(keys[i].IPWhitelist) {
			allAllowlisted = false
		}
	}
	status.CompliantKeyCount = compliant
	status.PendingKeyCount = len(keys) - compliant
	if compliant == len(keys) {
		status.Reason = KeyRotationReasonAllKeysRotated
		if allAllowlisted {
			status.Reason = KeyRotationReasonAllKeysAllowlist
		}
		return status, nil
	}

	status.Required = true
	status.Reason = KeyRotationReasonRotationRequired
	status.ErrorCode = KeyRotationRequiredCode
	status.Message = KeyRotationRequiredMessage
	return status, nil
}

// RequireUser evaluates the gate and returns ErrKeyRotationRequired when the
// user must remediate first. The boolean reports whether the request is blocked.
func (s *KeyRotationGateService) RequireUser(ctx context.Context, userID int64) (*KeyRotationGateStatus, bool, error) {
	status, err := s.Evaluate(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	return status, status.Required, nil
}
