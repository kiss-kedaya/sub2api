package service

import "context"

// WithCodexQuotaOverdraftScheduling snapshots the effective admin/config
// gates onto the request context.  All account attempts and failovers for the
// request then observe the same mode, even if settings are changed midway.
func (s *OpenAIGatewayService) WithCodexQuotaOverdraftScheduling(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	runtime := CodexQuotaOverdraftRuntimeSettings{}
	if s.settingService != nil {
		runtime = s.settingService.GetCodexQuotaOverdraftRuntime(ctx)
	} else if s.cfg != nil {
		runtime = CodexQuotaOverdraftRuntimeSettings{
			Enabled:                  s.cfg.Gateway.CodexQuotaOverdraftEnabled,
			BusinessInjectionEnabled: s.cfg.Gateway.CodexQuotaOverdraftBusinessInjectionEnabled,
		}
	}
	SetCodexQuotaOverdraftEnabled(runtime.Enabled)
	SetCodexQuotaOverdraftBusinessInjectionEnabled(runtime.BusinessInjectionEnabled)
	if !runtime.Enabled {
		return ctx
	}
	return WithCodexQuotaOverdraftSchedulingSnapshot(ctx, runtime.Enabled, runtime.BusinessInjectionEnabled)
}

func (s *OpenAIGatewayService) SetCodexQuotaOverdraftCoordinator(coordinator *CodexQuotaOverdraftCoordinator) {
	if s != nil {
		s.codexQuotaOverdraft = coordinator
	}
}

func (s *AccountUsageService) SetCodexQuotaOverdraftCoordinator(coordinator *CodexQuotaOverdraftCoordinator) {
	if s != nil {
		s.codexQuotaOverdraft = coordinator
	}
}

// ObserveCodexQuotaOverdraftScheduleSuccess records business evidence without
// changing the existing scheduler/health result contract. The request context
// is required so a client-supplied marker cannot be treated as evidence.
func (s *OpenAIGatewayService) ObserveCodexQuotaOverdraftScheduleSuccess(ctx context.Context, account *Account, model string) {
	if s == nil || account == nil || s.codexQuotaOverdraft == nil ||
		!codexQuotaOverdraftWasInjected(ctx, account.ID) {
		return
	}
	s.codexQuotaOverdraft.ObserveBusinessSuccess(account, model)
}

// codexQuotaOverdraftCoordinator returns the single coordinator owned by the
// OpenAI gateway. Keeping construction here avoids adding a fork-only provider
// to the generated Wire graph.
func (s *OpenAIGatewayService) codexQuotaOverdraftCoordinator(
	tlsFPProfileService *TLSFingerprintProfileService,
) *CodexQuotaOverdraftCoordinator {
	if s == nil {
		return nil
	}
	// Construct lazily whenever the gateway has the dependencies needed for the
	// feature.  The effective DB/config gate is checked by coordinator.enabled;
	// this lets an admin enable the detector without restarting the process.
	if s.accountRepo == nil || s.httpUpstream == nil {
		return nil
	}
	s.codexQuotaOverdraftOnce.Do(func() {
		if s.codexQuotaOverdraft != nil {
			return
		}
		var tempUnschedCache TempUnschedCache
		if s.rateLimitService != nil {
			tempUnschedCache = s.rateLimitService.tempUnschedCache
		}
		s.codexQuotaOverdraft = NewCodexQuotaOverdraftCoordinator(
			s.accountRepo,
			s.httpUpstream,
			s.openAITokenProvider,
			tlsFPProfileService,
			s.cfg,
			tempUnschedCache,
			s,
			s.rateLimitService,
		)
	})
	return s.codexQuotaOverdraft
}
