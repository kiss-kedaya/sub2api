package responseheaders

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestFilterHeadersDisabledUsesDefaultAllowlist(t *testing.T) {
	src := http.Header{}
	src.Add("Content-Type", "application/json")
	src.Add("X-Request-Id", "req-123")
	src.Add("X-Test", "ok")
	src.Add("Connection", "keep-alive")
	src.Add("Content-Length", "123")

	cfg := config.ResponseHeaderConfig{
		Enabled:     false,
		ForceRemove: []string{"x-request-id"},
	}

	filtered := FilterHeaders(src, CompileHeaderFilter(cfg))
	if filtered.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type passthrough, got %q", filtered.Get("Content-Type"))
	}
	if filtered.Get("X-Request-Id") != "req-123" {
		t.Fatalf("expected X-Request-Id allowed, got %q", filtered.Get("X-Request-Id"))
	}
	if filtered.Get("X-Test") != "" {
		t.Fatalf("expected X-Test removed, got %q", filtered.Get("X-Test"))
	}
	if filtered.Get("Connection") != "" {
		t.Fatalf("expected Connection to be removed, got %q", filtered.Get("Connection"))
	}
	if filtered.Get("Content-Length") != "" {
		t.Fatalf("expected Content-Length to be removed, got %q", filtered.Get("Content-Length"))
	}
}

func TestFilterHeadersAllowsReasoningIncludedByDefault(t *testing.T) {
	src := http.Header{}
	src.Set("X-Reasoning-Included", "1")

	filtered := FilterHeaders(src, CompileHeaderFilter(config.ResponseHeaderConfig{}))
	if got := filtered.Get("X-Reasoning-Included"); got != "1" {
		t.Fatalf("expected X-Reasoning-Included passthrough, got %q", got)
	}
}

func TestFilterHeadersForceRemoveOverridesReasoningIncluded(t *testing.T) {
	src := http.Header{}
	src.Set("X-Reasoning-Included", "1")

	filtered := FilterHeaders(src, CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled:     true,
		ForceRemove: []string{"x-reasoning-included"},
	}))
	if got := filtered.Get("X-Reasoning-Included"); got != "" {
		t.Fatalf("expected X-Reasoning-Included removal, got %q", got)
	}
}

func TestFilterHeadersEnabledUsesAllowlist(t *testing.T) {
	src := http.Header{}
	src.Add("Content-Type", "application/json")
	src.Add("X-Extra", "ok")
	src.Add("X-Remove", "nope")
	src.Add("X-Blocked", "nope")

	cfg := config.ResponseHeaderConfig{
		Enabled:           true,
		AdditionalAllowed: []string{"x-extra"},
		ForceRemove:       []string{"x-remove"},
	}

	filtered := FilterHeaders(src, CompileHeaderFilter(cfg))
	if filtered.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type allowed, got %q", filtered.Get("Content-Type"))
	}
	if filtered.Get("X-Extra") != "ok" {
		t.Fatalf("expected X-Extra allowed, got %q", filtered.Get("X-Extra"))
	}
	if filtered.Get("X-Remove") != "" {
		t.Fatalf("expected X-Remove removed, got %q", filtered.Get("X-Remove"))
	}
	if filtered.Get("X-Blocked") != "" {
		t.Fatalf("expected X-Blocked removed, got %q", filtered.Get("X-Blocked"))
	}
}

func TestFilterHeadersRejectsPrivateHeadersEvenWhenExplicitlyAllowed(t *testing.T) {
	names := []string{
		"Location", "Content-Location", "WWW-Authenticate", "Proxy-Authenticate",
		"Authentication-Info", "Proxy-Authentication-Info", "Authorization",
		"Proxy-Authorization", "Cookie", "Set-Cookie", "Set-Cookie2", "X-Api-Key",
		"ApiKey", "X-Goog-Api-Key", "X-Auth-Token", "X-Access-Token",
		"X-Provider-Secret", "X-Upstream-Credential", "X-Custom-Key", "Token",
	}
	src := http.Header{}
	for _, name := range names {
		src.Set(name, "private upstream metadata")
	}
	src.Set("X-Request-Id", "req-safe-123")

	for _, enabled := range []bool{false, true} {
		filtered := FilterHeaders(src, CompileHeaderFilter(config.ResponseHeaderConfig{
			Enabled:           enabled,
			AdditionalAllowed: names,
		}))
		for _, name := range names {
			if filtered.Get(name) != "" {
				t.Errorf("private response header %s passed through with filtering enabled=%v", name, enabled)
			}
		}
		if filtered.Get("X-Request-Id") != "req-safe-123" {
			t.Errorf("safe request correlation header was lost with filtering enabled=%v", enabled)
		}
	}
}

func TestFilterHeadersDropsPrivateValuesAndPreservesCodexStatus(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("X-Request-Id", "req-safe-123")
	src.Set("X-Reasoning-Included", "1")
	src.Set("X-Codex-Primary-Used-Percent", "12")
	src.Set("X-Codex-Primary-Reset-At", "1800000000")
	src.Set("X-Ratelimit-Remaining-Tokens", "4000")
	src.Set("Retry-After", "30")
	src.Add("X-Diagnostics", "https://provider.example.invalid/private")
	src.Add("X-Diagnostics", "upstream-owner@example.invalid")
	src.Add("X-Diagnostics", "Bearer sk-example-private-test-credential-1234567890")
	src.Add("X-Diagnostics", "ready")
	src.Set("X-Request-Secret", "private metadata")

	filtered := FilterHeaders(src, CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled: true,
		AdditionalAllowed: []string{
			"x-diagnostics", "x-request-secret", "x-codex-primary-used-percent", "x-codex-primary-reset-at",
		},
	}))
	if got := filtered.Values("X-Diagnostics"); len(got) != 1 || got[0] != "ready" {
		t.Fatal("private upstream diagnostic values were not removed")
	}
	if filtered.Get("X-Request-Secret") != "" {
		t.Fatal("a credential header bypassed the additional allowlist")
	}
	for _, name := range []string{
		"Content-Type", "X-Request-Id", "X-Reasoning-Included", "X-Codex-Primary-Used-Percent",
		"X-Codex-Primary-Reset-At", "X-Ratelimit-Remaining-Tokens", "Retry-After",
	} {
		if filtered.Get(name) != src.Get(name) {
			t.Errorf("safe response header %s was not preserved", name)
		}
	}
}

func TestWriteFilteredHeadersDoesNotExposePrivateMetadata(t *testing.T) {
	src := http.Header{}
	src.Set("Location", "https://provider.example.invalid/private")
	src.Set("WWW-Authenticate", `Bearer realm="provider.example.invalid"`)
	src.Set("X-Request-Id", "req-safe-123")
	dst := http.Header{}
	WriteFilteredHeaders(dst, src, nil)
	if dst.Get("Location") != "" || dst.Get("WWW-Authenticate") != "" {
		t.Fatal("upstream routing or authentication metadata reached the response")
	}
	if dst.Get("X-Request-Id") != "req-safe-123" {
		t.Fatal("safe request correlation was not preserved")
	}
}
