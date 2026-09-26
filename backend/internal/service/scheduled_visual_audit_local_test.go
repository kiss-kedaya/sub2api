//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type scheduledVisualAuditTransport struct{}

func (u scheduledVisualAuditTransport) Do(req *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	defer transport.CloseIdleConnections()
	return (&http.Client{Transport: transport, Timeout: 180 * time.Second}).Do(req)
}

func (u scheduledVisualAuditTransport) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestScheduledVisualAuditLocal(t *testing.T) {
	expected := os.Getenv("SCHEDULED_VISUAL_AUDIT_EXPECT_STATUS")
	if expected != "" && expected != "success" && expected != "degraded" && expected != "unknown" {
		t.Fatal("invalid expected visual audit status")
	}
	fixture := os.Getenv("SCHEDULED_VISUAL_AUDIT_ACCOUNT")
	dir := os.Getenv("SCHEDULED_QUALITY_SAMPLE_DIR")
	if fixture == "" || dir == "" {
		t.Skip("explicit local live audit only")
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal("fixture unavailable")
	}
	var row struct {
		ID            int64          `json:"id"`
		Platform      string         `json:"platform"`
		Type          string         `json:"type"`
		Credentials   map[string]any `json:"credentials"`
		Extra         map[string]any `json:"extra"`
		Concurrency   int            `json:"concurrency"`
		Model         string         `json:"model_id"`
		Prompt        string         `json:"prompt_text"`
		ProxyProtocol string         `json:"proxy_protocol"`
		ProxyHost     string         `json:"proxy_host"`
		ProxyPort     int            `json:"proxy_port"`
		ProxyUsername string         `json:"proxy_username"`
		ProxyPassword string         `json:"proxy_password"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(string(raw), "\ufeff")), &row) != nil {
		t.Fatal("invalid fixture")
	}
	account := &Account{ID: row.ID, Platform: row.Platform, Type: row.Type, Credentials: row.Credentials, Extra: row.Extra, Concurrency: row.Concurrency}
	if row.ProxyHost != "" {
		id := int64(1)
		account.ProxyID = &id
		account.Proxy = &Proxy{Protocol: row.ProxyProtocol, Host: row.ProxyHost, Port: row.ProxyPort, Username: row.ProxyUsername, Password: row.ProxyPassword}
	}
	repo := &scheduledQualityAccountRepo{account: account}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	svc := &AccountTestService{accountRepo: repo, httpUpstream: scheduledVisualAuditTransport{}, cfg: cfg}
	plan := &ScheduledTestPlan{AccountID: row.ID, ModelID: row.Model, PromptText: row.Prompt}
	pattern := os.Getenv("SCHEDULED_VISUAL_AUDIT_GLOB")
	if pattern == "" {
		pattern = "a29173-*.html"
	}
	var files []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(pattern, ",") {
		matches, err := filepath.Glob(filepath.Join(dir, strings.TrimSpace(part)))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			if !seen[match] {
				files = append(files, match)
				seen[match] = true
			}
		}
	}
	var out []map[string]string
	output := os.Getenv("SCHEDULED_VISUAL_AUDIT_OUTPUT")
	if output == "" {
		output = filepath.Join(dir, "account-29173-visual-classification.json")
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		status, reason := svc.assessScheduledVisualQuality(context.Background(), plan, string(body))
		out = append(out, map[string]string{"file": filepath.Base(f), "status": status, "reason": reason})
		t.Log(filepath.Base(f), status, reason)
		result, _ := json.MarshalIndent(out, "", "  ")
		if err := os.WriteFile(output, result, 0600); err != nil {
			t.Fatal(err)
		}
		if expected != "" && status != expected {
			t.Errorf("%s: expected %s, got %s (%s)", filepath.Base(f), expected, status, reason)
		}
	}
	if len(files) == 0 {
		t.Fatal("no audit samples")
	}
}
