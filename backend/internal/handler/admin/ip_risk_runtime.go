package admin

func (h *IPRiskHandler) Stop() {
	if h != nil && h.manager != nil {
		h.manager.Stop()
	}
}

func (h *IPRiskHandler) UseAuditHandler(audit *AuditLogHandler) {
	if audit != nil {
		h.audit = audit.auditService
	}
}
