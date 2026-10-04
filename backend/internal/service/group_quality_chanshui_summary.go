package service

import (
	"encoding/json"
	"math"
	"strings"
)

// The public channel page receives only these fields, never raw probes,
// request/response text, audit URLs, remote audit IDs or arbitrary reasons.
type ChanshuiPublicSection struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Score  *float64 `json:"score"`
}

type ChanshuiPublicSummary struct {
	Score          *float64                `json:"score"`
	CandidateModel string                  `json:"candidate_model"`
	Sections       []ChanshuiPublicSection `json:"sections"`
}

func publicAuditToken(value any) string {
	s, ok := value.(string)
	if !ok || len(s) > 128 || chanshuiKeyPattern.MatchString(s) {
		return ""
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.ContainsRune("-_.:/", c):
			continue
		default:
			return ""
		}
	}
	return s
}

func publicAuditScore(value any) *float64 {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
		return nil
	}
	return &n
}

// PopulateGroupQualityAudit derives the historical method from the RESULT,
// not the current plan, which may have switched providers since this run.
func PopulateGroupQualityAudit(event *GroupQualityEvent, raw string) {
	event.QualityProvider = QualityProviderPelican
	if event.Status != "audit_pass" && event.Status != "audit_fail" {
		return
	}
	event.QualityProvider = QualityProviderChanshui
	event.ErrorMessage = ""
	if event.Status == "audit_pass" {
		event.Status = "success"
	} else {
		event.Status = "degraded"
	}
	var data map[string]any
	if len(raw) > chanshuiMaxBody || json.Unmarshal([]byte(raw), &data) != nil || data["provider"] != QualityProviderChanshui {
		return
	}
	verdict := chanshuiObject(data["verdict"])
	summary := &ChanshuiPublicSummary{
		Score:          publicAuditScore(chanshuiObject(verdict["total"])["score"]),
		CandidateModel: publicAuditToken(chanshuiObject(verdict["fingerprint"])["top_model"]),
		Sections:       []ChanshuiPublicSection{},
	}
	for _, name := range []string{"fingerprint", "injection", "hidden", "cache", "tools", "web", "knowledge", "thinking", "iq"} {
		section := chanshuiObject(verdict[name])
		if len(section) == 0 {
			continue
		}
		summary.Sections = append(summary.Sections, ChanshuiPublicSection{Name: name, Status: publicAuditToken(section["status"]), Score: publicAuditScore(section["score"])})
	}
	event.AuditSummary = summary
}
