package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Chanshui decisions use their own statuses and per-plan scheduling ownership,
// separate from the Pelican two-consecutive-failures policy.
func (s *ScheduledTestRunnerService) runChanshuiPlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestResult, time.Duration, error) {
	repo, ok := s.planRepo.(ScheduledChanshuiRepository)
	if !ok {
		return nil, 0, fmt.Errorf("audit state storage unavailable")
	}
	state, err := repo.GetChanshuiAudit(ctx, plan.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot load audit state")
	}
	client := s.chanshuiClient
	if client == nil {
		client = newCustomUsageClient()
	}
	now := time.Now()
	finish := func(status, message string, data map[string]any, secret string) (*ScheduledTestResult, time.Duration, error) {
		started := now
		report := map[string]any{"provider": QualityProviderChanshui}
		if state != nil {
			started = state.StartedAt
			report["audit_id"] = state.ID
			report["expires_at"] = state.ExpiresAt
			report["skipped"] = state.Skipped
		}
		if data != nil {
			report["model"] = data["model"]
			report["verdict"] = data["verdict"]
			report["probes"] = data["probes"]
			report["audit_status"] = data["status"]
			if data["status"] == "done" {
				decision := ChanshuiDecision{"unknown", "检测期间配置发生变化，下一轮按当前停止条件重新判断"}
				if state != nil && state.DecisionConfigHash != "" && state.DecisionConfigHash == chanshuiDecisionConfigHash(plan) {
					decision = evaluateChanshuiStopCondition(plan, data)
				}
				report["decision"] = decision
				status = decision.Status
				message = decision.Reason
			}
		}
		fingerprint := ""
		if state != nil {
			fingerprint = state.KeyFingerprint
		}
		safe := redactChanshui(report, secret, fingerprint)
		raw, marshalErr := json.Marshal(safe)
		if marshalErr != nil {
			return nil, 0, fmt.Errorf("cannot encode audit report")
		}
		// State is cleared only AFTER the result has been durably saved by the runner.
		finished := time.Now()
		return &ScheduledTestResult{Status: status, ResponseText: string(raw), ErrorMessage: message, StartedAt: started, FinishedAt: finished, LatencyMs: finished.Sub(started).Milliseconds()}, 0, nil
	}
	if state != nil {
		if state.ID == "" && state.Status == "submitting" {
			return finish("unknown", "上次掺水提交在保存检测编号前中断，结果不确定；本轮不重复创建。", nil, "")
		}
		if now.Unix() >= state.ExpiresAt {
			return finish("unknown", "掺水检测已超过结果有效期，未重新创建检测。", nil, "")
		}
		pollURL, urlErr := chanshuiPollURL(state.ServiceURL, state.PollURL, state.ID)
		if urlErr != nil {
			return finish("unknown", "掺水轮询地址未通过同源校验。", nil, "")
		}
		code, headers, data, pollErr := chanshuiExchange(ctx, client, http.MethodGet, pollURL, nil)
		if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
			return nil, chanshuiRetryAfter(headers.Get("Retry-After")), nil
		}
		if code == http.StatusNotFound || code == http.StatusGone {
			return finish("unknown", "掺水检测不存在或已过期（结果保留24小时，服务重启后可能丢失）；未重试失败项。", nil, "")
		}
		if pollErr != nil || code >= 500 {
			return nil, time.Minute, nil
		}
		if code != http.StatusOK {
			return finish("unknown", fmt.Sprintf("掺水轮询返回 HTTP %d。", code), nil, "")
		}
		id, _ := data["id"].(string)
		status, _ := data["status"].(string)
		if id != state.ID || (status != "queued" && status != "running" && status != "done") {
			return finish("unknown", "掺水返回了无法识别的任务状态。", nil, "")
		}
		if status != "done" {
			state.Status = status
			if err := repo.SetChanshuiAudit(ctx, plan.ID, state); err != nil {
				return nil, 0, fmt.Errorf("cannot persist audit progress")
			}
			return nil, time.Minute, nil
		}
		secret := ""
		if s.accountTestSvc != nil && s.accountTestSvc.accountRepo != nil {
			if a, e := s.accountTestSvc.accountRepo.GetByID(ctx, plan.AccountID); e == nil && a != nil {
				secret = a.GetCredential("api_key")
			}
		}
		return finish("completed", "掺水检测完成。", data, secret)
	}
	if s.accountTestSvc == nil || s.accountTestSvc.accountRepo == nil {
		return nil, 0, fmt.Errorf("account reader unavailable")
	}
	account, err := s.accountTestSvc.accountRepo.GetByID(ctx, plan.AccountID)
	if err != nil {
		return finish("unknown", "无法读取被测账号。", nil, "")
	}
	copyPlan := *plan
	if err := normalizeScheduledQualityProvider(&copyPlan); err != nil {
		return finish("unknown", err.Error(), nil, "")
	}
	upstream, key, protocol, err := chanshuiUpstream(account, copyPlan.QualityConfig)
	if err != nil {
		return finish("unknown", err.Error(), nil, "")
	}
	allowed, err := repo.ReserveChanshuiCreate(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("audit submission rate limiter unavailable")
	}
	if !allowed {
		return nil, time.Minute, nil
	}
	sections := any("all")
	if len(copyPlan.QualityConfig.Sections) > 0 {
		sections = copyPlan.QualityConfig.Sections
	}
	payload := map[string]any{"base_url": upstream, "api_key": key, "model": plan.ModelID, "protocol": protocol, "timeout": copyPlan.QualityConfig.Timeout, "sections": sections}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot encode audit request")
	}
	endpoint := strings.TrimRight(copyPlan.QualityConfig.BaseURL, "/") + "/api/v1/audits"
	// Persist the non-secret intent before the non-idempotent POST. A crash after
	// remote acceptance but before storing its ID must not resubmit the same run.
	state = &ChanshuiAuditState{Status: "submitting", ServiceURL: copyPlan.QualityConfig.BaseURL, StartedAt: now, ExpiresAt: now.Add(24 * time.Hour).Unix(), KeyFingerprint: chanshuiFingerprint(key), DecisionConfigHash: chanshuiDecisionConfigHash(&copyPlan)}
	if err := repo.SetChanshuiAudit(ctx, plan.ID, state); err != nil {
		return nil, 0, fmt.Errorf("cannot persist audit submission intent")
	}
	code, headers, data, createErr := chanshuiExchange(ctx, client, http.MethodPost, endpoint, body)
	if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
		wait := chanshuiRetryAfter(headers.Get("Retry-After"))
		if err := repo.DelayChanshuiCreate(ctx, wait); err != nil {
			return nil, 0, fmt.Errorf("cannot persist audit Retry-After")
		}
		if err := repo.SetChanshuiAudit(ctx, plan.ID, nil); err != nil {
			return nil, 0, fmt.Errorf("cannot clear rejected audit intent")
		}
		return nil, wait, nil
	}
	if createErr != nil {
		return finish("unknown", "掺水创建结果不确定，本轮不重复提交。", nil, key)
	}
	if code != http.StatusAccepted {
		return finish("unknown", fmt.Sprintf("掺水创建返回 HTTP %d，本轮不重试。", code), nil, key)
	}
	id, _ := data["id"].(string)
	poll, _ := data["poll_url"].(string)
	expires, _ := data["expires_at"].(float64)
	status, _ := data["status"].(string)
	pollURL, err := chanshuiPollURL(copyPlan.QualityConfig.BaseURL, poll, id)
	if err != nil || expires <= float64(now.Unix()) || (status != "queued" && status != "running" && status != "done") {
		return finish("unknown", "掺水创建响应缺少有效的检测编号、轮询地址、状态或有效期；本轮不重复提交。", nil, key)
	}
	state = &ChanshuiAuditState{ID: id, PollURL: pollURL, ServiceURL: copyPlan.QualityConfig.BaseURL, StartedAt: now, ExpiresAt: int64(expires), KeyFingerprint: chanshuiFingerprint(key), Status: status, Skipped: redactChanshui(data["skipped"], key, ""), DecisionConfigHash: chanshuiDecisionConfigHash(&copyPlan)}
	if err := repo.SetChanshuiAudit(ctx, plan.ID, state); err != nil {
		return finish("unknown", "掺水已接受任务，但本地保存轮询状态失败；本轮不重复提交。", nil, key)
	}
	return nil, time.Minute, nil
}
