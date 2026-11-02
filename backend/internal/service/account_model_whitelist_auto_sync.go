package service

import (
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ModelWhitelistAutoSyncExtraKey is the account.extra key written by the admin
// batch-edit UI:
//
//	{"enabled": true, "interval_hours": 24, "mode": "whitelist"}
//
// Persistence contract (verified against the current repository layer):
//   - accounts.extra is JSONB and accepts arbitrary keys;
//   - BulkUpdate merges with `extra || $jsonb`, and UpdateAccountExtra merges the
//     same way, so this key survives unrelated bulk edits;
//   - a full single-account PUT (/admin/accounts/:id) REPLACES the whole extra
//     map, so this key must be in the preserved managed-key list below or any
//     later edit would silently drop it.
//
// Execution contract: this value is CONFIGURATION ONLY. No server-side scheduler
// reads it today; nothing will fetch upstream models on a timer. The immediate
// sync still requires the admin-triggered POST
// /admin/accounts/:id/models/sync-upstream. Clients must not present the
// interval as active until a runner exists.
const ModelWhitelistAutoSyncExtraKey = "model_whitelist_auto_sync"

// ModelWhitelistAutoSyncModeWhitelist is the only supported mirror mode: the
// fetched upstream catalog replaces the account's model whitelist.
const ModelWhitelistAutoSyncModeWhitelist = "whitelist"

const (
	modelWhitelistAutoSyncDefaultIntervalHours = 24
	modelWhitelistAutoSyncMinIntervalHours     = 1
	modelWhitelistAutoSyncMaxIntervalHours     = 720
)

var errModelWhitelistAutoSyncInvalid = infraerrors.BadRequest(
	"MODEL_WHITELIST_AUTO_SYNC_INVALID",
	"model_whitelist_auto_sync must be an object {enabled: bool, interval_hours: 1-720, mode: \"whitelist\"}",
)

// ValidateModelWhitelistAutoSyncExtra validates and normalizes the config in
// place so the stored JSON uses stable types (int hours, literal bool/mode).
// A nil value clears the key (explicit opt-out); an absent key is a no-op;
// unknown sibling keys inside the object are preserved for forward compatibility.
func ValidateModelWhitelistAutoSyncExtra(extra map[string]any) error {
	if extra == nil {
		return nil
	}
	raw, exists := extra[ModelWhitelistAutoSyncExtraKey]
	if !exists {
		return nil
	}
	if raw == nil {
		delete(extra, ModelWhitelistAutoSyncExtraKey)
		return nil
	}

	config, ok := raw.(map[string]any)
	if !ok {
		return errModelWhitelistAutoSyncInvalid
	}

	enabled := false
	if value, ok := config["enabled"]; ok && value != nil {
		flag, ok := value.(bool)
		if !ok {
			return errModelWhitelistAutoSyncInvalid
		}
		enabled = flag
	}

	interval := modelWhitelistAutoSyncDefaultIntervalHours
	if value, ok := config["interval_hours"]; ok && value != nil {
		parsed, ok := modelWhitelistAutoSyncHours(value)
		if !ok || parsed < modelWhitelistAutoSyncMinIntervalHours || parsed > modelWhitelistAutoSyncMaxIntervalHours {
			return errModelWhitelistAutoSyncInvalid
		}
		interval = parsed
	}

	mode := ModelWhitelistAutoSyncModeWhitelist
	if value, ok := config["mode"]; ok && value != nil {
		text, ok := value.(string)
		if !ok || text != ModelWhitelistAutoSyncModeWhitelist {
			return errModelWhitelistAutoSyncInvalid
		}
		mode = text
	}

	config["enabled"] = enabled
	config["interval_hours"] = interval
	config["mode"] = mode
	extra[ModelWhitelistAutoSyncExtraKey] = config
	return nil
}

// ModelWhitelistAutoSyncConfigView is the read-only projection returned by the
// admin endpoint. Field names match what the batch-edit UI writes.
type ModelWhitelistAutoSyncConfigView struct {
	Enabled       bool   `json:"enabled"`
	IntervalHours int    `json:"interval_hours"`
	Mode          string `json:"mode"`
}

// ModelWhitelistAutoSyncConfigFromExtra returns the stored configuration with
// defaults applied. ok=false means the account has no auto-sync object.
func ModelWhitelistAutoSyncConfigFromExtra(extra map[string]any) (ModelWhitelistAutoSyncConfigView, bool) {
	view := ModelWhitelistAutoSyncConfigView{
		IntervalHours: modelWhitelistAutoSyncDefaultIntervalHours,
		Mode:          ModelWhitelistAutoSyncModeWhitelist,
	}
	if extra == nil {
		return view, false
	}
	raw, exists := extra[ModelWhitelistAutoSyncExtraKey]
	if !exists || raw == nil {
		return view, false
	}
	config, ok := raw.(map[string]any)
	if !ok {
		return view, false
	}
	if value, ok := config["enabled"].(bool); ok {
		view.Enabled = value
	}
	if value, ok := config["interval_hours"]; ok && value != nil {
		if parsed, valid := modelWhitelistAutoSyncHours(value); valid {
			view.IntervalHours = parsed
		}
	}
	if value, ok := config["mode"].(string); ok && value != "" {
		view.Mode = value
	}
	return view, true
}

// ModelWhitelistAutoSyncStateFromExtra exposes the runner state to the admin
// endpoint. Nil means the account never ran.
func ModelWhitelistAutoSyncStateFromExtra(extra map[string]any) *ModelWhitelistAutoSyncState {
	return decodeModelWhitelistAutoSyncState(extra)
}
func modelWhitelistAutoSyncHours(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

// managedAccountExtraKeysPreservedOnUpdate lists keys owned by dedicated flows
// that a plain account edit must never overwrite by omission.
func managedAccountExtraKeysPreservedOnUpdate() []string {
	return []string{
		"quota_used",
		"quota_daily_used",
		"quota_daily_start",
		"quota_weekly_used",
		"quota_weekly_start",
		grokBillingExtraKey,
		UpstreamBillingProbeEnabledExtraKey,
		UpstreamBillingRateSyncEnabledExtraKey,
		CustomUsageExtraKey,
		UpstreamBillingProbeExtraKey,
		OllamaCloudUsageSessionExtraKey,
		OllamaCloudUsageAutoRefreshExtraKey,
		OllamaCloudUsageSnapshotExtraKey,
		OpenAIAutoResetCreditStateExtraKey,
		ModelWhitelistAutoSyncExtraKey,
	}
}

// preserveManagedAccountExtra copies managed keys from the stored account into
// the replacement map so a full PUT does not silently erase them.
func preserveManagedAccountExtra(account *Account, normalizedExtra map[string]any) {
	if account == nil || normalizedExtra == nil {
		return
	}
	for _, key := range managedAccountExtraKeysPreservedOnUpdate() {
		if value, ok := account.Extra[key]; ok {
			normalizedExtra[key] = value
		}
	}
}
