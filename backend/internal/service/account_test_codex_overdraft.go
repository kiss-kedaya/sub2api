package service

import (
	"context"
	"net/http"
	"time"
)

func (s *AccountTestService) SetCodexQuotaOverdraftCoordinator(coordinator *CodexQuotaOverdraftCoordinator) {
	if s != nil {
		s.codexQuotaOverdraft = coordinator
	}
}

func (s *AccountTestService) prepareCodexQuotaOverdraftTestRequest(ctx context.Context, account *Account, payload []byte) (context.Context, []byte, bool) {
	enabled, businessInjection := s.codexQuotaOverdraftTestRuntime(ctx)
	if !enabled || !isCodexQuotaOverdraftAccount(account) {
		return ctx, payload, false
	}
	ctx = WithCodexQuotaOverdraftSchedulingSnapshot(ctx, enabled, businessInjection)
	if !businessInjection || !codexQuotaOverdraftInjectionEligible(account, time.Now().UTC()) {
		return ctx, payload, false
	}
	updated, changed, err := injectCodexQuotaOverdraft(payload)
	if err != nil || !changed {
		return ctx, payload, false
	}
	markCodexQuotaOverdraftInjected(ctx, account.ID)
	return ctx, updated, true
}

func (s *AccountTestService) handleCodexQuotaOverdraftTest429(ctx context.Context, account *Account, headers http.Header, body []byte, preferredModel string) bool {
	enabled, _ := s.codexQuotaOverdraftTestRuntime(ctx)
	return s.codexQuotaOverdraft != nil && enabled && isCodexQuotaOverdraftAccount(account) &&
		s.codexQuotaOverdraft.HandleQuota429(ctx, account, headers, body, preferredModel)
}

func (s *AccountTestService) observeCodexQuotaOverdraftTestResult(ctx context.Context, account *Account, preferredModel string, injected bool) {
	enabled, _ := s.codexQuotaOverdraftTestRuntime(ctx)
	if s.codexQuotaOverdraft == nil || !enabled || !isCodexQuotaOverdraftAccount(account) {
		return
	}
	if injected {
		s.codexQuotaOverdraft.ObserveBusinessSuccess(account, preferredModel)
	} else {
		s.codexQuotaOverdraft.ObserveAccount(account, preferredModel)
	}
}

func (s *AccountTestService) codexQuotaOverdraftTestEnabled(account *Account) bool {
	enabled, _ := s.codexQuotaOverdraftTestRuntime(context.Background())
	return enabled && isCodexQuotaOverdraftAccount(account)
}

func (s *AccountTestService) codexQuotaOverdraftTestRuntime(ctx context.Context) (bool, bool) {
	if s == nil {
		return false, false
	}
	if s.settingService != nil {
		runtime := s.settingService.GetCodexQuotaOverdraftRuntime(ctx)
		return runtime.Enabled, runtime.BusinessInjectionEnabled
	}
	return CodexQuotaOverdraftEnabled(), CodexQuotaOverdraftBusinessInjectionEnabled()
}
