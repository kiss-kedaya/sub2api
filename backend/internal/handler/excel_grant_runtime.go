package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideExcelGrantHandler(grants *service.ExcelGrantService, channel *service.ExcelBPSChannel, gateway *service.OpenAIGatewayService, totp *service.TotpService, users *service.UserService) *admin.ExcelGrantHandler {
	grants.Start()
	gateway.SetExcelBPSChannel(channel)
	h := admin.NewExcelGrantHandler(grants)
	h.SetStepUpDependencies(totp, users)
	return h
}
