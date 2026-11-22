//go:build unit

package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type probeSettingsStub struct {
	settings AdminAuthProbeSettings
}

func (s *probeSettingsStub) GetAdminAuthProbeSettingsCached(context.Context) AdminAuthProbeSettings {
	return s.settings
}

type probeAlertWriterStub struct {
	mu       sync.Mutex
	rules    []*OpsAlertRule
	events   []*OpsAlertEvent
	nextRule int64
}

func (w *probeAlertWriterStub) ListAlertRules(context.Context) ([]*OpsAlertRule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*OpsAlertRule(nil), w.rules...), nil
}

func (w *probeAlertWriterStub) CreateAlertRule(_ context.Context, rule *OpsAlertRule) (*OpsAlertRule, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextRule++
	created := *rule
	created.ID = w.nextRule
	w.rules = append(w.rules, &created)
	return &created, nil
}

func (w *probeAlertWriterStub) CreateAlertEvent(_ context.Context, event *OpsAlertEvent) (*OpsAlertEvent, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	created := *event
	created.ID = int64(len(w.events) + 1)
	w.events = append(w.events, &created)
	return &created, nil
}

func (w *probeAlertWriterStub) eventsSnapshot() []*OpsAlertEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*OpsAlertEvent(nil), w.events...)
}

type probeAuditRecorderStub struct {
	mu      sync.Mutex
	entries []*AuditLog
}

func (r *probeAuditRecorderStub) Record(entry *AuditLog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
}

func (r *probeAuditRecorderStub) snapshot() []*AuditLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*AuditLog(nil), r.entries...)
}

func newProbeMonitor(t *testing.T, settings AdminAuthProbeSettings) (*AdminAuthProbeMonitor, *probeAlertWriterStub, *probeAuditRecorderStub) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	writer := &probeAlertWriterStub{}
	audit := &probeAuditRecorderStub{}
	monitor := NewAdminAuthProbeMonitor(client, &probeSettingsStub{settings: settings}, writer, audit)
	return monitor, writer, audit
}

func waitProbeEvents(t *testing.T, writer *probeAlertWriterStub, want int) []*OpsAlertEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if events := writer.eventsSnapshot(); len(events) >= want {
			return events
		}
		time.Sleep(5 * time.Millisecond)
	}
	return writer.eventsSnapshot()
}

func recordProbeFailure(monitor *AdminAuthProbeMonitor, ip, path string, status int) bool {
	return monitor.RecordAdminAuthFailure(context.Background(), AdminAuthProbeInput{
		ClientIP:   ip,
		Method:     "GET",
		Path:       path,
		StatusCode: status,
		UserAgent:  "probe-agent/1.0",
	})
}

func TestAdminAuthProbe_FiresAtThreshold(t *testing.T) {
	settings := *DefaultAdminAuthProbeSettings()
	settings.Threshold = 3
	settings.DistinctPaths = 100
	monitor, writer, audit := newProbeMonitor(t, settings)

	for i := 0; i < 2; i++ {
		if recordProbeFailure(monitor, "203.0.113.9", "/api/v1/admin/users", 401) {
			t.Fatalf("failure %d must stay below threshold", i+1)
		}
	}
	if events := writer.eventsSnapshot(); len(events) != 0 {
		t.Fatalf("expected no alert below threshold, got %d", len(events))
	}
	if entries := audit.snapshot(); len(entries) != 0 {
		t.Fatalf("expected no audit entry below threshold, got %d", len(entries))
	}

	if !recordProbeFailure(monitor, "203.0.113.9", "/api/v1/admin/users", 403) {
		t.Fatal("threshold hit must fire an alert")
	}
	entries := audit.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Action != AuditActionAdminAuthProbeAlert {
		t.Fatalf("unexpected audit action %q", entry.Action)
	}
	if entry.ClientIP != "203.0.113.9" || entry.Path != "/api/v1/admin/users" || entry.StatusCode != 403 {
		t.Fatalf("audit entry missing observed fields: %+v", entry)
	}
	if entry.UserAgent != "probe-agent/1.0" {
		t.Fatalf("audit entry UA = %q", entry.UserAgent)
	}
	if strings.Contains(entry.Path, "?") {
		t.Fatalf("audit path must not carry query string: %q", entry.Path)
	}

	events := waitProbeEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("expected one ops alert event, got %d", len(events))
	}
	event := events[0]
	if event.Severity != "P2" || event.Status != OpsAlertStatusFiring {
		t.Fatalf("unexpected event severity/status: %+v", event)
	}
	dimensions := event.Dimensions
	if dimensions["client_ip"] != "203.0.113.9" {
		t.Fatalf("event dimensions client_ip = %v", dimensions["client_ip"])
	}
	if dimensions["failure_count"] != int64(3) || dimensions["distinct_paths"] != int64(1) {
		t.Fatalf("event dimensions counts = %v / %v", dimensions["failure_count"], dimensions["distinct_paths"])
	}
	if dimensions["status_401"] != int64(2) || dimensions["status_403"] != int64(1) {
		t.Fatalf("event dimensions statuses = %v / %v", dimensions["status_401"], dimensions["status_403"])
	}
	paths, ok := dimensions["paths"].([]string)
	if !ok || len(paths) != 1 || paths[0] != "/api/v1/admin/users" {
		t.Fatalf("event dimensions paths = %#v", dimensions["paths"])
	}
	if _, leaked := dimensions["authorization"]; leaked {
		t.Fatal("event must not contain header values")
	}
}

func TestAdminAuthProbe_FiresOnDistinctPaths(t *testing.T) {
	settings := *DefaultAdminAuthProbeSettings()
	settings.Threshold = 1000
	settings.DistinctPaths = 3
	monitor, writer, _ := newProbeMonitor(t, settings)

	recordProbeFailure(monitor, "203.0.113.10", "/api/v1/admin/users", 401)
	recordProbeFailure(monitor, "203.0.113.10", "/api/v1/admin/accounts", 403)
	if events := writer.eventsSnapshot(); len(events) != 0 {
		t.Fatalf("two distinct paths must not fire with distinct_paths=3, got %d", len(events))
	}
	if !recordProbeFailure(monitor, "203.0.113.10", "/api/v1/admin/settings", 401) {
		t.Fatal("third distinct path must fire an alert")
	}
	events := waitProbeEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].Dimensions["distinct_paths"] != int64(3) {
		t.Fatalf("distinct_paths = %v", events[0].Dimensions["distinct_paths"])
	}
}

func TestAdminAuthProbe_DisabledDoesNotTouchRedis(t *testing.T) {
	settings := *DefaultAdminAuthProbeSettings()
	settings.Enabled = false
	settings.Threshold = 1
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	monitor := NewAdminAuthProbeMonitor(client, &probeSettingsStub{settings: settings}, &probeAlertWriterStub{}, &probeAuditRecorderStub{})

	if recordProbeFailure(monitor, "203.0.113.11", "/api/v1/admin/users", 401) {
		t.Fatal("disabled monitor must not fire")
	}
	if keys := mini.Keys(); len(keys) != 0 {
		t.Fatalf("disabled monitor must not write redis keys, got %v", keys)
	}
}

func TestAdminAuthProbe_DedupesWithinCooldown(t *testing.T) {
	settings := *DefaultAdminAuthProbeSettings()
	settings.Threshold = 2
	settings.DistinctPaths = 100
	monitor, writer, audit := newProbeMonitor(t, settings)

	recordProbeFailure(monitor, "203.0.113.12", "/api/v1/admin/users", 401)
	if !recordProbeFailure(monitor, "203.0.113.12", "/api/v1/admin/users", 401) {
		t.Fatal("threshold hit must fire")
	}
	// 冷却窗口内的后续失败只计数，不再重复写事件/审计。
	for i := 0; i < 5; i++ {
		if recordProbeFailure(monitor, "203.0.113.12", "/api/v1/admin/users", 403) {
			t.Fatal("dedupe window must suppress repeated alerts")
		}
	}
	events := waitProbeEvents(t, writer, 1)
	if len(events) != 1 {
		t.Fatalf("expected one event after dedupe, got %d", len(events))
	}
	if entries := audit.snapshot(); len(entries) != 1 {
		t.Fatalf("expected one audit entry after dedupe, got %d", len(entries))
	}
}

func TestAdminAuthProbe_TruncatesUserAgentAndStripsQuery(t *testing.T) {
	settings := *DefaultAdminAuthProbeSettings()
	settings.Threshold = 1
	monitor, _, audit := newProbeMonitor(t, settings)

	longUA := strings.Repeat("u", 200)
	monitor.RecordAdminAuthFailure(context.Background(), AdminAuthProbeInput{
		ClientIP:   "203.0.113.13",
		Method:     "post",
		Path:       "/api/v1/admin/accounts?token=secret#frag",
		StatusCode: 401,
		UserAgent:  longUA,
	})
	entries := audit.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected one audit entry, got %d", len(entries))
	}
	entry := entries[0]
	if len([]rune(entry.UserAgent)) != adminAuthProbeUAMaxRunes {
		t.Fatalf("UA must be truncated to %d runes, got %d", adminAuthProbeUAMaxRunes, len([]rune(entry.UserAgent)))
	}
	if entry.Path != "/api/v1/admin/accounts" {
		t.Fatalf("path must drop query/fragment, got %q", entry.Path)
	}
	if entry.Method != "POST" {
		t.Fatalf("method = %q", entry.Method)
	}
}

func TestNormalizeAdminProbeIP(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"203.0.113.1", "203.0.113.1", true},
		{"::ffff:203.0.113.1", "203.0.113.1", true},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::", true},
		{"", "", false},
		{"not-an-ip", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeAdminProbeIP(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Fatalf("normalizeAdminProbeIP(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestAdminAuthProbeSettings_DefaultsAndNormalize(t *testing.T) {
	defaults := DefaultAdminAuthProbeSettings()
	if !defaults.Enabled || defaults.Threshold != 20 || defaults.DistinctPaths != 5 || defaults.WindowMinutes != 5 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
	normalized := AdminAuthProbeSettings{}
	normalizeAdminAuthProbeSettings(&normalized)
	// 零值里 Enabled=false 视为显式关闭，其余数值字段补默认值。
	if normalized.Enabled {
		t.Fatal("zero settings keep Enabled=false; only numeric fields fall back to defaults")
	}
	if normalized.Threshold != defaults.Threshold || normalized.DistinctPaths != defaults.DistinctPaths || normalized.WindowMinutes != defaults.WindowMinutes || normalized.CooldownMinutes != defaults.CooldownMinutes {
		t.Fatalf("zero settings numeric fields must normalize to defaults, got %+v", normalized)
	}
	over := AdminAuthProbeSettings{Threshold: -1, DistinctPaths: adminAuthProbeDistinctPathsMax + 1, WindowMinutes: adminAuthProbeWindowMaxMinutes + 1}
	normalizeAdminAuthProbeSettings(&over)
	if over.Threshold != defaults.Threshold || over.DistinctPaths != adminAuthProbeDistinctPathsMax || over.WindowMinutes != adminAuthProbeWindowMaxMinutes {
		t.Fatalf("unexpected normalized over-limit settings: %+v", over)
	}
}

type adminProbeSettingRepo struct {
	values map[string]string
}

func (r *adminProbeSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *adminProbeSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *adminProbeSettingRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func (r *adminProbeSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *adminProbeSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *adminProbeSettingRepo) GetAll(context.Context) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *adminProbeSettingRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func TestAdminAuthProbeSettings_PersistAndCache(t *testing.T) {
	repo := &adminProbeSettingRepo{values: map[string]string{}}
	svc := &SettingService{settingRepo: repo}

	settings, err := svc.GetAdminAuthProbeSettings(context.Background())
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if !settings.Enabled || settings.Threshold != 20 {
		t.Fatalf("unexpected defaults: %+v", settings)
	}

	next := *settings
	next.Threshold = 7
	next.DistinctPaths = 3
	next.WindowMinutes = 10
	if err := svc.SetAdminAuthProbeSettings(context.Background(), &next); err != nil {
		t.Fatalf("set: %v", err)
	}
	stored, err := svc.GetAdminAuthProbeSettings(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Threshold != 7 || stored.DistinctPaths != 3 || stored.WindowMinutes != 10 {
		t.Fatalf("stored settings = %+v", stored)
	}

	// 缓存读路径立即拿到刚保存的值。
	cached := svc.GetAdminAuthProbeSettingsCached(context.Background())
	if cached.Threshold != 7 {
		t.Fatalf("cached settings = %+v", cached)
	}

	// 负数直接拒绝。
	invalid := next
	invalid.Threshold = -1
	if err := svc.SetAdminAuthProbeSettings(context.Background(), &invalid); err == nil {
		t.Fatal("negative threshold must be rejected")
	}
}
