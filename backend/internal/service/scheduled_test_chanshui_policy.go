package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

const ChanshuiQualityPausesExtraKey = "chanshui_quality_pauses"

type ChanshuiStopRule struct {
	Type      string   `json:"type"`
	Threshold *float64 `json:"threshold,omitempty"`
	Section   string   `json:"section,omitempty"`
	Status    string   `json:"status,omitempty"`
}
type ChanshuiStopCondition struct {
	Match         string             `json:"match"`
	ExpectedModel string             `json:"expected_model,omitempty"`
	Rules         []ChanshuiStopRule `json:"rules"`
}
type ChanshuiDecision struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func normalizeChanshuiStopCondition(c *ChanshuiConfig) error {
	if c.StopCondition == nil {
		c.StopCondition = &ChanshuiStopCondition{Match: "any", Rules: []ChanshuiStopRule{{Type: "fingerprint_mismatch"}}}
	}
	p := c.StopCondition
	if p.Match == "" {
		p.Match = "any"
	}
	p.ExpectedModel = strings.TrimSpace(p.ExpectedModel)
	if p.Match != "any" && p.Match != "all" {
		return fmt.Errorf("stop condition match must be any or all")
	}
	if len(p.Rules) == 0 || len(p.Rules) > 10 {
		return fmt.Errorf("configure between 1 and 10 stop conditions")
	}
	hasSection := func(section string) bool {
		if len(c.Sections) == 0 {
			return true
		}
		for _, s := range c.Sections {
			if s == section {
				return true
			}
		}
		return false
	}
	for i := range p.Rules {
		r := &p.Rules[i]
		switch r.Type {
		case "fingerprint_mismatch":
			if !hasSection("fingerprint") {
				return fmt.Errorf("fingerprint stop condition requires the fingerprint section")
			}
		case "total_score_below":
			if r.Threshold == nil || math.IsNaN(*r.Threshold) || math.IsInf(*r.Threshold, 0) || *r.Threshold < 0 || *r.Threshold > 100 {
				return fmt.Errorf("total-score threshold must be between 0 and 100")
			}
			if len(c.Sections) == 1 && c.Sections[0] == "knowledge" {
				return fmt.Errorf("knowledge cutoff is not a scored section")
			}
		case "section_status":
			allowed := map[string]bool{"tools": true, "web": true, "cache": true, "hidden": true, "knowledge": true}
			r.Status = strings.TrimSpace(r.Status)
			if !allowed[r.Section] || !hasSection(r.Section) {
				return fmt.Errorf("status condition requires its selected supported section")
			}
			if r.Status == "" || len(r.Status) > 64 || strings.ContainsAny(r.Status, "\r\n") {
				return fmt.Errorf("section status must be a non-empty literal value")
			}
		default:
			return fmt.Errorf("unsupported stop condition")
		}
	}
	return nil
}

func chanshuiDecisionConfigHash(plan *ScheduledTestPlan) string {
	copyPlan := *plan
	if err := normalizeScheduledQualityProvider(&copyPlan); err != nil {
		return ""
	}
	raw, err := json.Marshal(struct {
		Model  string
		Config ChanshuiConfig
	}{copyPlan.ModelID, copyPlan.QualityConfig})
	if err != nil {
		return ""
	}
	return chanshuiFingerprint(string(raw))
}

func chanshuiObject(v any) map[string]any { o, _ := v.(map[string]any); return o }
func chanshuiProbeIncomplete(data map[string]any, section string) bool {
	probes, _ := data["probes"].([]any)
	for _, p := range probes {
		o := chanshuiObject(p)
		id, _ := o["id"].(string)
		status, _ := o["status"].(string)
		if section != "" && id != section {
			continue
		}
		if status == "queued" || status == "running" || status == "error" || status == "failed" {
			return true
		}
	}
	return false
}

// Rules are typed comparisons over verified API fields, never executable code.
// A known stop condition can pause immediately; missing evidence cannot recover.
func evaluateChanshuiStopCondition(plan *ScheduledTestPlan, data map[string]any) ChanshuiDecision {
	copyPlan := *plan
	if err := normalizeScheduledQualityProvider(&copyPlan); err != nil {
		return ChanshuiDecision{"unknown", "停止条件配置无效"}
	}
	p := copyPlan.QualityConfig.StopCondition
	verdict := chanshuiObject(data["verdict"])
	matched, known := 0, 0
	reasons := make([]string, 0, len(p.Rules))
	for _, r := range p.Rules {
		hit, available := false, false
		switch r.Type {
		case "fingerprint_mismatch":
			candidate, _ := chanshuiObject(verdict["fingerprint"])["top_model"].(string)
			expected := p.ExpectedModel
			if expected == "" {
				expected = plan.ModelID
			}
			if strings.TrimSpace(candidate) != "" && !chanshuiProbeIncomplete(data, "fingerprint") {
				available = true
				hit = !strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(expected))
				if hit {
					reasons = append(reasons, "模型指纹候选与期望模型不一致")
				}
			}
		case "total_score_below":
			total := chanshuiObject(verdict["total"])
			score, ok := total["score"].(float64)
			iqIncomplete, _ := chanshuiObject(verdict["iq"])["complete"].(bool)
			_, hasIQ := chanshuiObject(verdict["iq"])["complete"]
			if ok && score >= 0 && score <= 100 && !chanshuiProbeIncomplete(data, "") && (!hasIQ || iqIncomplete) {
				available = true
				hit = score < *r.Threshold
				if hit {
					reasons = append(reasons, fmt.Sprintf("总分 %.2f 低于阈值 %.2f", score, *r.Threshold))
				}
			}
		case "section_status":
			status, _ := chanshuiObject(verdict[r.Section])["status"].(string)
			status = strings.TrimSpace(status)
			if status != "" && !chanshuiProbeIncomplete(data, r.Section) {
				hit = strings.EqualFold(status, r.Status)
				unknown := status == "unknown" || status == "queued" || status == "running" || status == "skipped" || status == "error"
				available = hit || !unknown
				if hit {
					reasons = append(reasons, fmt.Sprintf("板块 %s 状态命中 %s", r.Section, r.Status))
				}
			}
		}
		if available {
			known++
			if hit {
				matched++
			}
		}
	}
	if (p.Match == "any" && matched > 0) || (p.Match == "all" && matched == len(p.Rules)) {
		return ChanshuiDecision{"audit_fail", strings.Join(reasons, "；")}
	}
	if known < len(p.Rules) {
		return ChanshuiDecision{"unknown", "检测证据不足，保持当前调度状态"}
	}
	return ChanshuiDecision{"audit_pass", "停止条件未命中，可解除本计划造成的暂停"}
}

type ScheduledChanshuiAccountState interface {
	ApplyChanshuiQualityState(context.Context, int64, int64, time.Time, bool) (bool, error)
}

func HasChanshuiQualityPause(account *Account) bool {
	if account == nil {
		return false
	}
	pauses, _ := account.Extra[ChanshuiQualityPausesExtraKey].(map[string]any)
	return len(pauses) > 0
}
