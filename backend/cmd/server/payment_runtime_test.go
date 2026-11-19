package main

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestDelegatePaymentsSkipsLocalPaymentRuntime(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{DelegatePayments: true},
		Totp:   config.TotpConfig{EncryptionKeyConfigured: true},
	}
	// The detached role must return before touching any of these dependencies.
	runtime := provideLocalCryptoRuntime(cfg, nil, nil, nil, nil, nil, nil)
	if runtime.CryptoService != nil || runtime.CryptoHandler != nil || runtime.CryptoDepositHandler != nil {
		t.Fatal("local wallet and crypto handlers were constructed on the gateway")
	}
	if provideLocalPaymentOrderExpiry(cfg, nil, nil, nil) != nil {
		t.Fatal("local payment expiry worker was constructed on the gateway")
	}
	if service.CryptoUserAPIReady(cfg) {
		t.Fatal("gateway with external payments advertised local crypto support")
	}
}
