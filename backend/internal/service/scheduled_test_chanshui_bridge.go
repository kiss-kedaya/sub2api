package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/chanshui"
)

// ChanshuiQualityPausesExtraKey marks per-plan ownership of an account pause;
// multiple failing plans must each pass before the account can resume.
const ChanshuiQualityPausesExtraKey = "chanshui_quality_pauses"

// scheduledQualityReasonPrefix is the legacy pause-reason stamp from the
// removed pelican reviewer. Only cleanup of stored history still reads it.
const scheduledQualityReasonPrefix = "scheduled_quality_check:"

func HasChanshuiQualityPause(account *Account) bool {
	if account == nil {
		return false
	}
	pauses, _ := account.Extra[ChanshuiQualityPausesExtraKey].(map[string]any)
	return len(pauses) > 0
}

// normalizeScheduledQualityProvider validates the plan's quality provider.
// The pelican visual reviewer was removed: legacy rows degrade to plain
// connectivity plans instead of re-enabling a deleted evaluator.
func normalizeScheduledQualityProvider(plan *ScheduledTestPlan) error {
	if plan.QualityProvider == "" {
		plan.QualityProvider = QualityProviderChanshui
	}
	if plan.QualityProvider != QualityProviderChanshui {
		plan.QualityProvider = QualityProviderChanshui
		plan.QualityCheckEnabled = false
		return nil
	}
	return plan.QualityConfig.Normalize(plan.ModelID)
}

// chanshuiDecisionConfigHash fingerprints the model+config pair so a pending
// audit can only apply its decision while the policy that created it is
// unchanged.
func chanshuiDecisionConfigHash(plan *ScheduledTestPlan) string {
	if plan == nil {
		return ""
	}
	copyPlan := *plan
	if err := normalizeScheduledQualityProvider(&copyPlan); err != nil {
		return ""
	}
	return chanshui.DecisionConfigHash(copyPlan.ModelID, copyPlan.QualityConfig)
}

// chanshuiUpstream resolves the audit target from the pinned account: the
// account's own upstream base URL and key are sent to the audit service.
func chanshuiUpstream(account *Account, config ChanshuiConfig) (string, string, string, error) {
	if account == nil || account.Type != AccountTypeAPIKey || account.IsCredentialShadow() {
		return "", "", "", fmt.Errorf("chanshui requires a directly configured API-key account")
	}
	key := strings.TrimSpace(account.GetCredential("api_key"))
	if key == "" {
		return "", "", "", fmt.Errorf("account has no upstream API key")
	}
	protocol := config.Protocol
	base := strings.TrimSpace(account.GetCredential("base_url"))
	switch {
	case account.IsAdaptiveAPIProtocol():
		selected := APIProtocolChatCompletions
		switch protocol {
		case "anthropic":
			selected = APIProtocolAnthropic
		case "openai":
			selected = APIProtocolResponses
		}
		base = account.GetCNProtocolBaseURL(selected)
	case account.IsAnthropicProtocol():
		base = account.GetAnthropicProtocolBaseURL()
	case account.Platform == PlatformAnthropic:
		base = account.GetBaseURL()
	case account.Platform == PlatformGrok:
		base = account.GetGrokBaseURL()
	case base == "":
		base = account.GetOpenAIBaseURL()
	}
	u, err := customUsageValidateURL(base)
	if err != nil || u.RawQuery != "" {
		return "", "", "", fmt.Errorf("account needs a public upstream base URL")
	}
	return base, key, protocol, nil
}
