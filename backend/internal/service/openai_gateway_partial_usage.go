package service

import "errors"

// openAIForwardResultHasObservedBillingUnits reports whether a partial stream
// result contains upstream usage that must survive a terminal read error.
func openAIForwardResultHasObservedBillingUnits(result *OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	return openAIUsageHasTokens(&result.Usage) || result.ImageCount > 0 ||
		result.SearchCount > 0 || result.WebSearchCalls > 0 ||
		result.VideoCount > 0 || result.AudioUsage != nil
}

// preserveOpenAIStreamingResultOnError mirrors the Anthropic partial-usage
// invariant: an attempt eligible for replay never exposes a billable result,
// while a terminal stream error keeps usage already reported by the upstream.
//
// 例外——流式补扣失败主动中止（ErrBalanceWithholdingFailed）：此时上游确已交付部分
// 输出（越过预留窗口才触发补扣），但终帧未到故 result 无 observed usage。若按下面
// 的"无 usage 即丢弃"规则返回 nil，handler 的 submitUsage(nil) 空操作会跳过
// RecordUsage/guard.Finalize，仅剩 defer 全额退款 → 已交付输出被免费漏扣。故对该
// 哨兵错误保留 result（即使 usage=0），使其照常进入统一计费任务。
// 上游未返回 usage 时 actual=0，该任务释放整笔预扣，不得把 hold
// 伪造成实际消费。failover 可重放错误仍恒返回 nil，避免重试成功后双重计费。
func preserveOpenAIStreamingResultOnError(result *OpenAIForwardResult, err error) (*OpenAIForwardResult, error) {
	if err == nil {
		return result, nil
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return nil, err
	}
	if result != nil && errors.Is(err, ErrBalanceWithholdingFailed) {
		return result, err
	}
	if !openAIForwardResultHasObservedBillingUnits(result) {
		return nil, err
	}
	return result, err
}
