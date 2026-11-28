package service

import "errors"

// openAIForwardResultHasObservedBillingUnits reports whether a partial stream
// result contains upstream usage that must survive a terminal read error.
func openAIForwardResultHasObservedBillingUnits(result *OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	return openAIUsageHasTokens(&result.Usage) || result.ImageCount > 0 ||
		result.SearchCount > 0 || result.FetchCount > 0 || result.WebSearchCalls > 0 ||
		result.VideoCount > 0 || result.AudioUsage != nil || result.OCRPages > 0
}

// markOpenAIStreamEndedWithoutTerminal marks a replayable failover raised because
// the upstream ended the stream without a terminal frame after it had accepted the
// request. The upstream may already have billed that attempt (new-api bills the
// merged prompt at the plain input price on an abnormal end), so the handler is
// allowed to settle the observed usage if no retry succeeds. Replayability itself
// is unchanged: the flag only decides whether the partial result is exposed.
func markOpenAIStreamEndedWithoutTerminal(err *UpstreamFailoverError) *UpstreamFailoverError {
	if err != nil {
		err.StreamEndedWithoutTerminal = true
	}
	return err
}

// preserveOpenAIStreamingResultOnError mirrors the Anthropic partial-usage
// invariant: an attempt eligible for replay never exposes a billable result, while a
// terminal stream error keeps usage already reported by the upstream.
//
// 例外一——上游在没有终止帧的情况下结束流（StreamEndedWithoutTerminal）：上游已经
// 接受并很可能结算了这次请求，只是重试可能在下游成功。把估算结果暴露给 handler，
// 由 handler 在「重试真的成功」时丢弃、在穷尽时入账，避免整条调用免费漏扣
// （issue #456）。可重放的普通失败（连接被拒、上游明确报错）仍恒返回 nil。
//
// 例外二——流式补扣失败主动中止（ErrBalanceWithholdingFailed）：此时上游确已交付部分
// 输出（越过预留窗口才触发补扣），但终帧未到故 result 无 observed usage。若按下面
// 的"无 usage 即丢弃"规则返回 nil，handler 的 submitUsage(nil) 空操作会跳过
// RecordUsage/guard.Finalize，仅剩 defer 全额退款 → 已交付输出被免费漏扣。故对该
// 哨兵错误保留 result（即使 usage=0），使其照常进入统一计费任务。
// 上游未返回 usage 时 actual=0，该任务释放整笔预扣，不得把 hold
// 伪造成实际消费。
func preserveOpenAIStreamingResultOnError(result *OpenAIForwardResult, err error) (*OpenAIForwardResult, error) {
	if err == nil {
		return result, nil
	}
	if result != nil && result.NonBillableUpstreamError && errors.Is(err, errOpenAIStreamSilenceLimit) {
		return result, err
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		if failoverErr.StreamEndedWithoutTerminal && openAIForwardResultHasObservedBillingUnits(result) {
			return result, err
		}
		return nil, err
	}
	if result != nil && isBalanceHoldTopUpError(err) {
		return result, err
	}
	if !openAIForwardResultHasObservedBillingUnits(result) {
		return nil, err
	}
	return result, err
}
