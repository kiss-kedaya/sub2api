package service

func ProvideCredentialGuardService(repo CredentialGuardRepository, accounts AccountRepository, prober *OpenAIGatewayService, encryptor SecretEncryptor, invalidator TokenCacheInvalidator) *CredentialGuardService {
	svc := NewCredentialGuardService(repo, accounts, prober, encryptor, invalidator)
	startBackgroundJob(svc.Start)
	return svc
}
