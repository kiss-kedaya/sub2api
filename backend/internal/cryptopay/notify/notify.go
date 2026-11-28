// Package notify delivers crypto wallet operation events to the operator's
// channels: a generic JSON webhook (Bark-compatible) and email.
//
// Delivery is strictly best effort. A crypto signature, sweep or configuration
// change must never fail or stall because a phone or a mail server is down, so
// events are handed to a bounded queue and delivered by a background worker.
// Delivery failures are logged and dropped; they never reach the caller.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// DefaultTimeout bounds one delivery attempt. It is deliberately short: the
// notification path is asynchronous, but a worker stuck on a dead endpoint
// would still make every later event late.
const DefaultTimeout = 5 * time.Second

// DefaultQueueSize bounds the number of queued events. Crypto admin operations
// are rare, so a full queue means the configured receiver is gone, not that
// the operator is busy: dropping is safer than growing memory.
const DefaultQueueSize = 64

// Event is the structured payload of one crypto operation. It must never carry
// shard material, mnemonics, private keys, TOTP seeds or the vault data key.
type Event struct {
	Event   string         `json:"event"`
	At      time.Time      `json:"at"`
	ActorID int64          `json:"actor_user_id,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// Notifier publishes one event. Implementations must not block the caller and
// must not panic.
type Notifier interface {
	Notify(ctx context.Context, ev Event)
}

// Config is the resolved operator notification configuration for one event.
// A zero Config means "notifications are turned off".
type Config struct {
	Enabled    bool
	WebhookURL string
	Email      string
}

// ConfigProvider loads the current notification configuration. It is called by
// the delivery worker, never by the caller, so a slow settings read cannot
// delay a signature.
type ConfigProvider func(ctx context.Context) (Config, error)

// EmailSender is the narrow slice of the project's mail sender that this
// package needs. service.EmailService satisfies it.
type EmailSender interface {
	SendEmail(ctx context.Context, to, subject, body string) error
}

// Sink is one delivery channel.
type Sink interface {
	Name() string
	Send(ctx context.Context, cfg Config, ev Event) error
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// WithSinks sets the delivery channels. Nil sinks are ignored.
func WithSinks(sinks ...Sink) Option {
	return func(d *Dispatcher) {
		for _, sink := range sinks {
			if sink != nil {
				d.sinks = append(d.sinks, sink)
			}
		}
	}
}

// WithTimeout overrides DefaultTimeout.
func WithTimeout(timeout time.Duration) Option {
	return func(d *Dispatcher) {
		if timeout > 0 {
			d.timeout = timeout
		}
	}
}

// WithQueueSize overrides DefaultQueueSize.
func WithQueueSize(size int) Option {
	return func(d *Dispatcher) {
		if size > 0 {
			d.queue = make(chan Event, size)
		}
	}
}

// WithLogger overrides the logger used for delivery failures.
func WithLogger(logger *slog.Logger) Option {
	return func(d *Dispatcher) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// Dispatcher is the asynchronous, bounded Notifier used in production.
type Dispatcher struct {
	provider ConfigProvider
	sinks    []Sink
	timeout  time.Duration
	logger   *slog.Logger

	queue chan Event
	done  chan struct{}
	once  sync.Once
}

// NewDispatcher starts the delivery worker. Callers should keep one Dispatcher
// for the process lifetime and Close it on shutdown.
func NewDispatcher(provider ConfigProvider, opts ...Option) *Dispatcher {
	dispatcher := &Dispatcher{
		provider: provider,
		timeout:  DefaultTimeout,
		logger:   slog.Default(),
		queue:    make(chan Event, DefaultQueueSize),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(dispatcher)
		}
	}
	go dispatcher.worker()
	return dispatcher
}

// Notify enqueues one event and returns immediately. When the queue is full
// the event is dropped with a warning instead of blocking the operation.
func (d *Dispatcher) Notify(ctx context.Context, ev Event) {
	if d == nil || d.provider == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	select {
	case d.queue <- ev:
	default:
		d.logger.Warn("[cryptopay] notification queue full; event dropped", "event", ev.Event)
	}
}

// Close stops the worker. Queued events may be lost; that is acceptable for a
// best-effort notification channel during shutdown.
func (d *Dispatcher) Close() {
	if d == nil {
		return
	}
	d.once.Do(func() { close(d.done) })
}

func (d *Dispatcher) worker() {
	for {
		select {
		case ev := <-d.queue:
			d.deliver(ev)
		case <-d.done:
			return
		}
	}
}

func (d *Dispatcher) deliver(ev Event) {
	defer func() {
		if recovered := recover(); recovered != nil {
			d.logger.Error("[cryptopay] notification delivery panicked", "event", ev.Event)
		}
	}()
	// The caller's request context is almost always canceled by the time a
	// queued event is delivered; a detached, bounded context is what the
	// outbound request must use.
	base := context.WithoutCancel(context.Background())
	ctx, cancel := context.WithTimeout(base, d.timeout)
	defer cancel()

	cfg, err := d.provider(ctx)
	if err != nil {
		d.logger.Warn("[cryptopay] notification config unavailable", "event", ev.Event, "cause", err.Error())
		return
	}
	if !cfg.Enabled {
		return
	}
	for _, sink := range d.sinks {
		if err := sink.Send(ctx, cfg, ev); err != nil {
			d.logger.Warn("[cryptopay] notification delivery failed", "event", ev.Event, "channel", sink.Name(), "cause", err.Error())
		}
	}
}

// WebhookSink POSTs one JSON document to a generic operator endpoint. A Bark
// device URL (https://api.day.app/<device-key>) works as-is because the payload
// carries the title/body fields Bark expects in addition to the structured
// event fields.
type WebhookSink struct {
	client       *http.Client
	customClient bool
}

// NewWebhookSink returns a webhook sink with its own timeout-bounded,
// outbound-safe HTTP client (blocks private/loopback/metadata hosts and
// disables 3xx redirects so bearer device URLs cannot leak or SSRF).
func NewWebhookSink() *WebhookSink {
	return &WebhookSink{client: newSafeWebhookHTTPClient()}
}

// NewWebhookSinkWithClient returns a webhook sink using the supplied client
// (intended for unit tests injecting an httptest.Server client). Passing nil
// falls back to the default outbound-safe client.
func NewWebhookSinkWithClient(client *http.Client) *WebhookSink {
	if client == nil {
		return NewWebhookSink()
	}
	return &WebhookSink{client: client, customClient: true}
}

func newSafeWebhookHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           urlvalidator.NewSafeDialContext(urlvalidator.OutboundPolicy{}),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: DefaultTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Timeout:   DefaultTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (s *WebhookSink) Name() string { return "webhook" }

func containsControlOrBackslash(raw string) bool {
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b < 0x20 || b == 0x7f || b == '\\' {
			return true
		}
	}
	return false
}

func isNonCanonicalIPv4Host(host string) bool {
	if host == "" {
		return false
	}
	parts := strings.Split(host, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 0, 64); err == nil {
			continue
		}
		allDigits := true
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				allDigits = false
				break
			}
		}
		if !allDigits {
			return false
		}
	}
	return true
}

func (s *WebhookSink) Send(ctx context.Context, cfg Config, ev Event) error {
	endpoint := strings.Trim(cfg.WebhookURL, " ")
	if endpoint == "" {
		return nil
	}
	if containsControlOrBackslash(endpoint) {
		return fmt.Errorf("webhook_url contains invalid characters")
	}
	if !s.customClient {
		if parsed, err := url.Parse(endpoint); err == nil {
			host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(parsed.Hostname())), ".")
			if _, parseErr := netip.ParseAddr(host); parseErr != nil && isNonCanonicalIPv4Host(host) {
				return fmt.Errorf("webhook_url rejected: %w", urlvalidator.ErrOutboundTargetNotAllowed)
			}
		}
		if _, err := urlvalidator.ValidateOutboundURL(ctx, endpoint, urlvalidator.OutboundPolicy{AllowInsecureHTTP: true}); err != nil {
			return fmt.Errorf("webhook_url rejected: %w", err)
		}
	} else {
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("webhook_url must be an absolute http(s) URL")
		}
	}
	payload, err := json.Marshal(webhookPayload(ev))
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		// Avoid echoing the webhook URL (which may carry a Bark device key).
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return fmt.Errorf("post webhook: %w", urlErr.Err)
		}
		return fmt.Errorf("post webhook: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}

// webhookPayload keeps the documented Event fields and adds the plain
// title/body pair that Bark-style receivers require.
func webhookPayload(ev Event) map[string]any {
	payload := map[string]any{
		"event": ev.Event,
		"at":    ev.At,
		"title": Title(ev),
		"body":  Body(ev),
	}
	if ev.ActorID > 0 {
		payload["actor_user_id"] = ev.ActorID
	}
	if len(ev.Detail) > 0 {
		payload["detail"] = ev.Detail
	}
	return payload
}

// EmailSink sends one plain-text mail through the project's existing mail
// sender. An empty recipient keeps the channel disabled.
type EmailSink struct {
	sender EmailSender
}

// NewEmailSink returns an email sink. A nil sender is tolerated and turns the
// sink into a no-op.
func NewEmailSink(sender EmailSender) *EmailSink {
	return &EmailSink{sender: sender}
}

func (s *EmailSink) Name() string { return "email" }

func (s *EmailSink) Send(ctx context.Context, cfg Config, ev Event) error {
	recipient := strings.TrimSpace(cfg.Email)
	if recipient == "" || s == nil || s.sender == nil {
		return nil
	}
	return s.sender.SendEmail(ctx, recipient, Title(ev), Body(ev))
}

// eventLabels are the human-readable operation names. Unknown events fall back
// to the raw event id so a new event is still readable.
var eventLabels = map[string]string{
	"signer.setup":              "分片创建",
	"signer.resplit":            "重新分片",
	"signer.verify_share":       "分片校验",
	"signer.unlock":             "签名器解锁",
	"signer.lock":               "签名器锁定",
	"sweep.destination_changed": "归集地址变更",
	"sweep.executed":            "归集执行",
}

// Title is the short human-readable event name.
func Title(ev Event) string {
	label, ok := eventLabels[ev.Event]
	if !ok {
		label = ev.Event
	}
	return "[sub2api] 钱包操作：" + label
}

// Body renders the plain-text message used by email and Bark. Only the fields
// passed in Detail are rendered; the caller is responsible for keeping them
// free of key material.
func Body(ev Event) string {
	var builder strings.Builder
	_, _ = builder.WriteString("事件：")
	_, _ = builder.WriteString(ev.Event)
	if label, ok := eventLabels[ev.Event]; ok {
		_, _ = builder.WriteString("（")
		_, _ = builder.WriteString(label)
		_, _ = builder.WriteString("）")
	}
	_, _ = builder.WriteString("\n时间：")
	_, _ = builder.WriteString(ev.At.UTC().Format(time.RFC3339))
	_, _ = builder.WriteString("\n操作人：")
	if ev.ActorID > 0 {
		_, _ = builder.WriteString("#")
		_, _ = builder.WriteString(strconv.FormatInt(ev.ActorID, 10))
	} else {
		_, _ = builder.WriteString("系统")
	}
	for _, key := range sortedDetailKeys(ev.Detail) {
		_, _ = builder.WriteString("\n")
		_, _ = builder.WriteString(key)
		_, _ = builder.WriteString("：")
		_, _ = builder.WriteString(formatDetailValue(ev.Detail[key]))
	}
	return builder.String()
}

func sortedDetailKeys(detail map[string]any) []string {
	keys := make([]string, 0, len(detail))
	for key := range detail {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatDetailValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}
