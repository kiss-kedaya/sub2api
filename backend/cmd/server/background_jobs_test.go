package main

import (
	"context"
	"testing"
)

func TestStartOptionalBackgroundServicesDisabled(t *testing.T) {
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	pluginStarted, promptAuditStarted := false, false

	startOptionalBackgroundServices(
		func(context.Context) error { pluginStarted = true; return nil },
		func(context.Context) error { promptAuditStarted = true; return nil },
	)

	if pluginStarted || promptAuditStarted {
		t.Fatalf("background services started with disable switch: plugin=%t prompt_audit=%t", pluginStarted, promptAuditStarted)
	}
}

func TestStartOptionalBackgroundServicesDefault(t *testing.T) {
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "")
	pluginStarted, promptAuditStarted := false, false

	startOptionalBackgroundServices(
		func(context.Context) error { pluginStarted = true; return nil },
		func(context.Context) error { promptAuditStarted = true; return nil },
	)

	if !pluginStarted || !promptAuditStarted {
		t.Fatalf("background services did not preserve default startup: plugin=%t prompt_audit=%t", pluginStarted, promptAuditStarted)
	}
}
