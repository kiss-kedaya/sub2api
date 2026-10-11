// Package bark sends Bark (iOS push) notifications. The wire format mirrors
// the epusdt barkwatch client: POST {base}/{device_key} with form fields.
package bark

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.day.app"

// Notification is one push.
type Notification struct {
	Title    string
	Subtitle string
	Body     string
	Group    string
	// Level: "active" rings, "timeSensitive" bypasses focus, empty is silent.
	Level string
}

// Sender delivers notifications.
type Sender interface {
	Send(ctx context.Context, n Notification) error
}

// Client is an HTTP Bark sender bound to one device key.
type Client struct {
	baseURL   string
	deviceKey string
	http      *http.Client
}

// New builds a client. An empty device key returns nil so callers can treat
// notifications as optional without branching everywhere.
func New(deviceKey string) *Client {
	deviceKey = strings.TrimSpace(deviceKey)
	if deviceKey == "" {
		return nil
	}
	return &Client{
		baseURL:   defaultBaseURL,
		deviceKey: deviceKey,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

// SetBaseURL overrides the API host (self-hosted Bark servers, tests).
func (c *Client) SetBaseURL(base string) {
	if c == nil {
		return
	}
	c.baseURL = strings.TrimRight(strings.TrimSpace(base), "/")
}

// Send delivers one notification. It never panics on a nil client so
// notification paths can stay branch-free.
func (c *Client) Send(ctx context.Context, n Notification) error {
	if c == nil {
		return nil
	}
	form := url.Values{}
	form.Set("title", n.Title)
	if n.Subtitle != "" {
		form.Set("subtitle", n.Subtitle)
	}
	if n.Body != "" {
		form.Set("body", n.Body)
	}
	if n.Group != "" {
		form.Set("group", n.Group)
	}
	if n.Level != "" {
		form.Set("level", n.Level)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/"+url.PathEscape(c.deviceKey),
		strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("bark request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("bark send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bark send: http %d", resp.StatusCode)
	}
	return nil
}
