package service

func ProvideOpsAutomationService(settings SettingRepository, groups GroupRepository, repo OpsAutomationRepository, tester *AccountTestService, gateway *OpenAIGatewayService, accounts AccountRepository, slots *ConcurrencyService, moderation *ContentModerationService) *OpsAutomationService {
	svc := NewOpsAutomationService(settings, groups, repo, tester)
	svc.SetQualityJudge(accounts, groups, slots, tester, moderation)
	gateway.SetOpsAutomationService(svc)
	if BackgroundJobsDisabled() {
		svc.StartGateway()
	} else {
		svc.Start()
	}
	return svc
}

func (s *OpenAIGatewayService) SetOpsAutomationService(ops *OpsAutomationService) {
	s.opsAutomation = ops
}
