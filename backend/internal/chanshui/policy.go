package chanshui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// StopRule is one typed comparison over verified audit fields. Rules are
// declarative data, never executable code.
type StopRule struct {
	Type      string   `json:"type"`
	Threshold *float64 `json:"threshold,omitempty"`
	Section   string   `json:"section,omitempty"`
	Status    string   `json:"status,omitempty"`
}

// StopCondition combines rules with any/all matching.
type StopCondition struct {
	Match         string     `json:"match"`
	ExpectedModel string     `json:"expected_model,omitempty"`
	Rules         []StopRule `json:"rules"`
}

// Decision is the outcome of evaluating a completed audit against a policy.
type Decision struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func normalizeStopCondition(c *Config) error {
	if c.StopCondition == nil {
		c.StopCondition = &StopCondition{Match: "any", Rules: []StopRule{{Type: "fingerprint_mismatch"}}}
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

// DecisionConfigHash fingerprints the model+config pair so a pending audit
// can only apply its decision while the policy that created it is unchanged.
func DecisionConfigHash(modelID string, c Config) string {
	raw, err := json.Marshal(struct {
		Model  string
		Config Config
	}{modelID, c})
	if err != nil {
		return ""
	}
	return Fingerprint(string(raw))
}

func object(v any) map[string]any { o, _ := v.(map[string]any); return o }

func probeIncomplete(data map[string]any, section string) bool {
	probes, _ := data["probes"].([]any)
	for _, p := range probes {
		o := object(p)
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

// EvaluateStopCondition decides a completed audit against the plan's policy.
// Rules are typed comparisons over verified API fields, never executable code.
// A known stop condition can pause immediately; missing evidence cannot recover.
func EvaluateStopCondition(modelID string, c Config, data map[string]any) Decision {
	copy := c
	if err := copy.Normalize(modelID); err != nil {
		return Decision{Status: "unknown", Reason: "停止条件配置无效"}
	}
	p := copy.StopCondition
	verdict := object(data["verdict"])
	matched, known := 0, 0
	reasons := make([]string, 0, len(p.Rules))
	for _, r := range p.Rules {
		hit, available := false, false
		switch r.Type {
		case "fingerprint_mismatch":
			candidate, _ := object(verdict["fingerprint"])["top_model"].(string)
			expected := p.ExpectedModel
			if expected == "" {
				expected = modelID
			}
			if strings.TrimSpace(candidate) != "" && !probeIncomplete(data, "fingerprint") {
				available = true
				hit = !strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(expected))
				if hit {
					reasons = append(reasons, "模型指纹候选与期望模型不一致")
				}
			}
		case "total_score_below":
			total := object(verdict["total"])
			score, ok := total["score"].(float64)
			iqIncomplete, _ := object(verdict["iq"])["complete"].(bool)
			_, hasIQ := object(verdict["iq"])["complete"]
			if ok && score >= 0 && score <= 100 && !probeIncomplete(data, "") && (!hasIQ || iqIncomplete) {
				available = true
				hit = score < *r.Threshold
				if hit {
					reasons = append(reasons, fmt.Sprintf("总分 %.2f 低于阈值 %.2f", score, *r.Threshold))
				}
			}
		case "section_status":
			status, _ := object(verdict[r.Section])["status"].(string)
			status = strings.TrimSpace(status)
			if status != "" && !probeIncomplete(data, r.Section) {
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
		return Decision{Status: "audit_fail", Reason: strings.Join(reasons, "；")}
	}
	if known < len(p.Rules) {
		return Decision{Status: "unknown", Reason: "检测证据不足，保持当前调度状态"}
	}
	return Decision{Status: "audit_pass", Reason: "停止条件未命中，可解除本计划造成的暂停"}
}
