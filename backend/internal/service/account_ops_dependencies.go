package service

// New notification destinations are opt-in and separate from the existing Bark
// and balance mail services. Enabling this service does not change their rules.
func (s *RateLimitService) SetAccountOpsService(ops *AccountOpsService) {
	if s != nil {
		s.accountOps = ops
	}
}

func (s *UpstreamBillingProbeService) SetAccountOpsService(ops *AccountOpsService) {
	if s != nil {
		s.accountOps = ops
	}
}

// Balance reads share the already opted-in billing probe's credentials,
// validation, route and CAS. Existing probes do not acquire extra requests unless
// this account has an enabled threshold rule in the notification configuration.
func (s *AccountOpsService) NeedsBalanceProbe(accountID int64) bool {
	if s == nil {
		return false
	}
	c := s.currentConfig()
	if !c.Enabled {
		return false
	}
	for _, rule := range c.BalanceThresholds {
		if rule.AccountID == accountID && rule.Enabled {
			return true
		}
	}
	return false
}
