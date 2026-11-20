package service

import (
	"encoding/json"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
)

// GPT-6.2 Ultra Fast 放行规则。
//
// 上游按 ChatGPT 订阅套餐放行（Pro 200 只开放 Sol，Pro 500 开放 Sol + Astra）。
// 本仓库没有 Pro 200/500 这种订阅套餐模型，因此这里不硬编码套餐，而是把放行
// 规则做成可配置白名单：
//
//	OPENAI_ULTRAFAST_ENTITLEMENTS='[
//	  {"plan":"pro","models":["gpt-6.2-sol"]},
//	  {"plan":"promax","models":["gpt-6.2-sol","gpt-6.2-astra"]}
//	]'
//
// 约定：
//   - 未配置（空串）或规则列表为空 → 不限制，所有支持 Ultra Fast 的模型/账号放行；
//   - plan 为空或 "*" → 不限制套餐；plan 按账号凭据 plan_type 归一化后比较
//     （忽略大小写、空格、"-"、"_"，pro/chatgpt_pro 视为同一档）；
//   - models 为空 → 该套餐可用全部支持 Ultra Fast 的模型；否则按基础模型 ID
//     （gpt-6.2-sol / gpt-6.2-astra）做精确或 "*" 通配匹配；
//   - 配置解析失败按「未配置」处理（放行），并打一条告警，避免一处 JSON 手误
//     把线上 Ultra Fast 全部掐断。
const openAIUltrafastEntitlementsEnv = "OPENAI_ULTRAFAST_ENTITLEMENTS"

// OpenAIUltrafastEntitlementRule 是放行白名单的一条规则。
type OpenAIUltrafastEntitlementRule struct {
	Plan   string   `json:"plan,omitempty"`
	Models []string `json:"models,omitempty"`
}

// openAIUltrafastEntitlementWarned 保证同一份坏配置每进程只告警一次。
var openAIUltrafastEntitlementWarned sync.Map

// openAIUltrafastEntitlementRules 读取并解析放行白名单；未配置返回 nil。
func openAIUltrafastEntitlementRules() []OpenAIUltrafastEntitlementRule {
	raw := strings.TrimSpace(os.Getenv(openAIUltrafastEntitlementsEnv))
	if raw == "" {
		return nil
	}
	var rules []OpenAIUltrafastEntitlementRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		if _, warned := openAIUltrafastEntitlementWarned.LoadOrStore(raw, struct{}{}); !warned {
			slog.Warn("invalid OpenAI Ultra Fast entitlement config; treating it as unrestricted",
				"env", openAIUltrafastEntitlementsEnv, "error", err)
		}
		return nil
	}
	return rules
}

// openAIUltrafastEntitled 判断账号是否有权使用某个支持 Ultra Fast 的基础模型。
// 未配置放行规则时始终返回 true。
func openAIUltrafastEntitled(account *Account, baseModel string) bool {
	rules := openAIUltrafastEntitlementRules()
	if len(rules) == 0 {
		return true
	}
	baseModel = strings.ToLower(strings.TrimSpace(baseModel))
	if baseModel == "" {
		return false
	}
	plan := ""
	if account != nil {
		plan = normalizeOpenAIUltrafastPlan(account.GetCredential("plan_type"))
	}
	for _, rule := range rules {
		if !openAIUltrafastPlanMatches(rule.Plan, plan) {
			continue
		}
		if openAIUltrafastModelsMatch(rule.Models, baseModel) {
			return true
		}
	}
	return false
}

// normalizeOpenAIUltrafastPlan 归一化账号凭据/配置里的套餐名，使 pro、
// chatgpt_pro、ChatGPT-Pro 等写法指向同一档。
func normalizeOpenAIUltrafastPlan(plan string) string {
	plan = strings.ToLower(strings.TrimSpace(plan))
	plan = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(plan)
	switch plan {
	case "chatgptpro":
		return "pro"
	case "chatgptpromax":
		return "promax"
	}
	return plan
}

func openAIUltrafastPlanMatches(rulePlan, accountPlan string) bool {
	rulePlan = strings.TrimSpace(rulePlan)
	if rulePlan == "" || rulePlan == "*" {
		return true
	}
	return normalizeOpenAIUltrafastPlan(rulePlan) == accountPlan
}

func openAIUltrafastModelsMatch(patterns []string, baseModel string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		switch {
		case pattern == "":
			continue
		case pattern == "*" || pattern == baseModel:
			return true
		case strings.ContainsAny(pattern, "*?["):
			if matched, err := path.Match(pattern, baseModel); err == nil && matched {
				return true
			}
		}
	}
	return false
}
