package service

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const QualityProviderPelican = "pelican"
const QualityProviderChanshui = "chanshui"
const DefaultChanshuiBaseURL = "https://chanshui.dev"

// BaseURL is the audit service, NOT the upstream URL sent in POST base_url.
// API keys are resolved from the pinned account only at request time.
type ChanshuiConfig struct {
	BaseURL       string                 `json:"base_url"`
	Protocol      string                 `json:"protocol"`
	Timeout       int                    `json:"timeout"`
	Sections      []string               `json:"sections"`
	StopCondition *ChanshuiStopCondition `json:"stop_condition,omitempty"`
}

func normalizeScheduledQualityProvider(plan *ScheduledTestPlan) error {
	if plan.QualityProvider == "" {
		plan.QualityProvider = QualityProviderPelican
	}
	if plan.QualityProvider != QualityProviderPelican && plan.QualityProvider != QualityProviderChanshui {
		return fmt.Errorf("unsupported quality provider")
	}
	c := &plan.QualityConfig
	if c.BaseURL == "" {
		c.BaseURL = DefaultChanshuiBaseURL
	}
	if c.Protocol == "" {
		c.Protocol = "auto"
	}
	if c.Timeout == 0 {
		c.Timeout = 360
	}
	if c.Sections == nil {
		c.Sections = []string{"fingerprint"}
	}
	if plan.QualityProvider != QualityProviderChanshui {
		return nil
	}
	if strings.TrimSpace(plan.ModelID) == "" {
		return fmt.Errorf("audit model is required")
	}
	u, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("audit service URL must not contain credentials, query parameters or a fragment")
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(c.BaseURL, urlvalidator.ValidationOptions{})
	if err != nil {
		return fmt.Errorf("audit service must use a public HTTPS URL")
	}
	c.BaseURL = normalized
	if c.Protocol != "auto" && c.Protocol != "chat" && c.Protocol != "anthropic" && c.Protocol != "openai" {
		return fmt.Errorf("unsupported audit protocol")
	}
	if c.Timeout < 10 || c.Timeout > 600 {
		return fmt.Errorf("audit timeout must be between 10 and 600 seconds")
	}
	allowed := map[string]bool{"fingerprint": true, "injection": true, "hidden": true, "cache": true, "tools": true, "web": true, "knowledge": true, "thinking": true, "iq": true}
	seen := map[string]bool{}
	sections := make([]string, 0, len(c.Sections))
	for _, section := range c.Sections {
		if !allowed[section] {
			return fmt.Errorf("unsupported audit section")
		}
		if !seen[section] {
			sections = append(sections, section)
			seen[section] = true
		}
	}
	c.Sections = sections // Empty selection means the API's documented "all" mode.
	if len(sections) == 1 && sections[0] == "thinking" && !strings.HasPrefix(plan.ModelID, "claude-") {
		return fmt.Errorf("thinking-only audits require a claude- model")
	}
	return normalizeChanshuiStopCondition(c)
}
