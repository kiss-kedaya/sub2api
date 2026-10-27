package service

import "testing"

func TestAccountOpsBalanceProbeRequiresExplicitEnabledRule(t *testing.T) {
	svc := NewAccountOpsService(nil, nil, nil)
	if svc.NeedsBalanceProbe(7) {
		t.Fatal("default notification settings must not add upstream calls")
	}
	cfg := defaultAccountOpsConfig()
	cfg.BalanceThresholds = []AccountOpsBalanceRule{{AccountID: 7, Enabled: true, Unit: "USD", Threshold: 5}}
	svc.config.Store(cfg)
	if svc.NeedsBalanceProbe(7) {
		t.Fatal("a saved threshold must not bypass the global off switch")
	}
	cfg.Enabled = true
	svc.config.Store(cfg)
	if !svc.NeedsBalanceProbe(7) || svc.NeedsBalanceProbe(8) {
		t.Fatal("only the opted-in account may receive balance probes")
	}
	cfg.BalanceThresholds[0].Enabled = false
	svc.config.Store(cfg)
	if svc.NeedsBalanceProbe(7) {
		t.Fatal("turning off the rule must stop extra upstream calls")
	}
}
