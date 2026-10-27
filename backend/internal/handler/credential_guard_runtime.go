package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideCredentialGuardHandler(svc *service.CredentialGuardService, totp *service.TotpService, users *service.UserService) *admin.CredentialGuardHandler {
	h := admin.NewCredentialGuardHandler(svc)
	h.SetStepUpDependencies(totp, users)
	return h
}
