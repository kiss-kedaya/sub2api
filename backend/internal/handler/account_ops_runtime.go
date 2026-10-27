package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideAccountOpsHandler(svc *service.AccountOpsService, email *service.EmailService, totp *service.TotpService, users *service.UserService) *admin.AccountOpsHandler {
	h := admin.NewAccountOpsHandler(svc, email)
	h.SetStepUpDependencies(totp, users)
	return h
}
