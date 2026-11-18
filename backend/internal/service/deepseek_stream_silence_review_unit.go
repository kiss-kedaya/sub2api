//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"time"
)

// SetDeepSeekStreamSilenceLimitForTest lets external-package handler regressions
// exercise the real transport without exporting production configuration.
func SetDeepSeekStreamSilenceLimitForTest(limit time.Duration) func() {
	previous := deepSeekStreamSilenceLimit
	deepSeekStreamSilenceLimit = limit
	return func() { deepSeekStreamSilenceLimit = previous }
}

// InstallDeepSeekSilenceScheduleObserverForTest preserves real selection while
// exposing health reports made by handlers after the upstream was accepted.
func InstallDeepSeekSilenceScheduleObserverForTest(s *OpenAIGatewayService) (func() int32, func()) {
	old, _ := openAIAdvancedSchedulerSettingCache.Load().(*cachedOpenAIAdvancedSchedulerSetting)
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{enabled: true, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	observer := &deepSeekSilenceScheduleObserver{OpenAIAccountScheduler: newDefaultOpenAIAccountScheduler(s, newOpenAIAccountRuntimeStats())}
	s.openaiScheduler = observer
	return observer.reports.Load, func() {
		if old == nil {
			old = &cachedOpenAIAdvancedSchedulerSetting{}
		}
		openAIAdvancedSchedulerSettingCache.Store(old)
	}
}

type deepSeekSilenceScheduleObserver struct {
	OpenAIAccountScheduler
	reports atomic.Int32
}

func (s *deepSeekSilenceScheduleObserver) ReportResult(int64, bool, *int) { s.reports.Add(1) }

// NewDeepSeekSilenceGuardForTest supplies a real ownership/finalization guard
// backed only by the handler's synthetic lifecycle repository and wallet.
func NewDeepSeekSilenceGuardForTest(repo UsageBillingRepository, wallet interface {
	AuthorizeLiveBalance(context.Context, int64, string, float64, float64) (LiveBalanceResult, error)
	TopUpLiveBalance(context.Context, int64, string, float64) (LiveBalanceResult, error)
	FinalizeLiveBalance(context.Context, int64, string, float64) (LiveBalanceResult, error)
	RefundLiveBalance(context.Context, int64, string) (LiveBalanceResult, error)
}) *BalancePreauthorizationGuard {
	return &BalancePreauthorizationGuard{core: &balancePreauthorizationGuardCore{
		service:   &BalancePreauthorizationService{repo: repo, wallet: wallet},
		requestID: "synthetic_silence_hold", apiKeyID: 55, userID: 77, attemptID: "synthetic_hold", holdAmount: 1,
		ownerToken: 1, terminalState: balancePreauthorizationGuardActive,
	}, ownerToken: 1}
}
