//go:build unit || opsalert_unit

package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSelectContiguousMetrics_Contiguous(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	metrics := []OpsMetrics{
		{UpdatedAt: now},
		{UpdatedAt: now.Add(-1 * time.Minute)},
		{UpdatedAt: now.Add(-2 * time.Minute)},
	}

	selected, ok := selectContiguousMetrics(metrics, 3, now)
	require.True(t, ok)
	require.Len(t, selected, 3)
}

func TestSelectContiguousMetrics_GapFails(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	metrics := []OpsMetrics{
		{UpdatedAt: now},
		// Missing the -1m sample (gap ~=2m).
		{UpdatedAt: now.Add(-2 * time.Minute)},
		{UpdatedAt: now.Add(-3 * time.Minute)},
	}

	_, ok := selectContiguousMetrics(metrics, 3, now)
	require.False(t, ok)
}

func TestSelectContiguousMetrics_StaleNewestFails(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)
	metrics := []OpsMetrics{
		{UpdatedAt: now.Add(-10 * time.Minute)},
		{UpdatedAt: now.Add(-11 * time.Minute)},
	}

	_, ok := selectContiguousMetrics(metrics, 2, now)
	require.False(t, ok)
}

func TestMetricValue_SuccessRate_NoTrafficIsNoData(t *testing.T) {
	metric := OpsMetrics{
		RequestCount: 0,
		SuccessRate:  0,
	}
	value, ok := metricValue(metric, OpsMetricSuccessRate)
	require.False(t, ok)
	require.Equal(t, 0.0, value)
}

func TestOpsAlertService_StopWithoutStart_NoPanic(t *testing.T) {
	s := NewOpsAlertService(nil, nil, nil)
	require.NotPanics(t, func() { s.Stop() })
}

func TestOpsAlertService_StartStop_Graceful(t *testing.T) {
	s := NewOpsAlertService(nil, nil, nil)
	s.interval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.StartWithContext(ctx)

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(1 * time.Second):
		t.Fatal("Stop did not return; background goroutine likely stuck")
	}

	require.NotPanics(t, func() { s.Stop() })
}

func TestBuildWebhookHTTPClient_DefaultTimeout(t *testing.T) {
	client := buildWebhookHTTPClient(nil, nil)
	require.Equal(t, webhookHTTPClientTimeout, client.Timeout)
	require.NotNil(t, client.CheckRedirect)
	require.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)

	base := &http.Client{}
	client = buildWebhookHTTPClient(base, nil)
	require.Equal(t, webhookHTTPClientTimeout, client.Timeout)
	require.NotNil(t, client.CheckRedirect)

	base = &http.Client{Timeout: 2 * time.Second}
	client = buildWebhookHTTPClient(base, nil)
	require.Equal(t, 2*time.Second, client.Timeout)
	require.NotNil(t, client.CheckRedirect)
}

func TestValidateWebhookURL_RequiresHTTPS(t *testing.T) {
	oldLookup := lookupIPAddrs
	t.Cleanup(func() { lookupIPAddrs = oldLookup })
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	_, err := validateWebhookURL(context.Background(), "http://example.com/webhook")
	require.Error(t, err)
}

func TestValidateWebhookURL_InvalidFormatRejected(t *testing.T) {
	_, err := validateWebhookURL(context.Background(), "https://[::1")
	require.Error(t, err)
}

func TestValidateWebhookURL_RejectsUserinfo(t *testing.T) {
	oldLookup := lookupIPAddrs
	t.Cleanup(func() { lookupIPAddrs = oldLookup })
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	_, err := validateWebhookURL(context.Background(), "https://user:pass@example.com/webhook")
	require.Error(t, err)
}

func TestValidateWebhookURL_RejectsLocalhost(t *testing.T) {
	_, err := validateWebhookURL(context.Background(), "https://localhost/webhook")
	require.Error(t, err)
}

func TestValidateWebhookURL_RejectsPrivateIPLiteral(t *testing.T) {
	cases := []string{
		"https://0.0.0.0/webhook",
		"https://127.0.0.1/webhook",
		"https://10.0.0.1/webhook",
		"https://192.168.1.2/webhook",
		"https://172.16.0.1/webhook",
		"https://172.31.255.255/webhook",
		"https://100.64.0.1/webhook",
		"https://169.254.169.254/webhook",
		"https://198.18.0.1/webhook",
		"https://224.0.0.1/webhook",
		"https://240.0.0.1/webhook",
		"https://[::]/webhook",
		"https://[::1]/webhook",
		"https://[ff02::1]/webhook",
	}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			_, err := validateWebhookURL(context.Background(), tc)
			require.Error(t, err)
		})
	}
}

func TestValidateWebhookURL_RejectsPrivateIPViaDNS(t *testing.T) {
	oldLookup := lookupIPAddrs
	t.Cleanup(func() { lookupIPAddrs = oldLookup })
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		require.Equal(t, "internal.example", host)
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.2")}}, nil
	}

	_, err := validateWebhookURL(context.Background(), "https://internal.example/webhook")
	require.Error(t, err)
}

func TestValidateWebhookURL_RejectsLinkLocalIPViaDNS(t *testing.T) {
	oldLookup := lookupIPAddrs
	t.Cleanup(func() { lookupIPAddrs = oldLookup })
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		require.Equal(t, "metadata.example", host)
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	_, err := validateWebhookURL(context.Background(), "https://metadata.example/webhook")
	require.Error(t, err)
}

func TestValidateWebhookURL_AllowsPublicHostViaDNS(t *testing.T) {
	oldLookup := lookupIPAddrs
	t.Cleanup(func() { lookupIPAddrs = oldLookup })
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		require.Equal(t, "example.com", host)
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	target, err := validateWebhookURL(context.Background(), "https://example.com:443/webhook")
	require.NoError(t, err)
	require.Equal(t, "https", target.URL.Scheme)
	require.Equal(t, "example.com", target.URL.Hostname())
	require.Equal(t, "443", target.URL.Port())
}

func TestValidateWebhookURL_RejectsInvalidPort(t *testing.T) {
	oldLookup := lookupIPAddrs
	t.Cleanup(func() { lookupIPAddrs = oldLookup })
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	_, err := validateWebhookURL(context.Background(), "https://example.com:99999/webhook")
	require.Error(t, err)
}

func TestWebhookTransport_UsesPinnedIP_NoDNSRebinding(t *testing.T) {
	oldLookup := lookupIPAddrs
	oldDial := webhookBaseDialContext
	t.Cleanup(func() {
		lookupIPAddrs = oldLookup
		webhookBaseDialContext = oldDial
	})

	lookupCalls := 0
	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		lookupCalls++
		require.Equal(t, "example.com", host)
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	target, err := validateWebhookURL(context.Background(), "https://example.com/webhook")
	require.NoError(t, err)
	require.Equal(t, 1, lookupCalls)

	lookupIPAddrs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		lookupCalls++
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
	}

	var dialAddrs []string
	webhookBaseDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialAddrs = append(dialAddrs, addr)
		return nil, errors.New("dial blocked in test")
	}

	client := buildWebhookHTTPClient(nil, target)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)

	_, err = transport.DialContext(context.Background(), "tcp", "example.com:443")
	require.Error(t, err)
	require.Equal(t, []string{"93.184.216.34:443"}, dialAddrs)
	require.Equal(t, 1, lookupCalls, "dial path must not re-resolve DNS")
}

func TestRetryWithBackoff_SucceedsAfterRetries(t *testing.T) {
	oldSleep := opsAlertSleep
	t.Cleanup(func() { opsAlertSleep = oldSleep })

	var slept []time.Duration
	opsAlertSleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	attempts := 0
	err := retryWithBackoff(
		context.Background(),
		3,
		[]time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		func() error {
			attempts++
			if attempts <= 3 {
				return errors.New("send failed")
			}
			return nil
		},
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, 4, attempts)
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, slept)
}

func TestRetryWithBackoff_ContextCanceledStopsRetries(t *testing.T) {
	oldSleep := opsAlertSleep
	t.Cleanup(func() { opsAlertSleep = oldSleep })

	var slept []time.Duration
	opsAlertSleep = func(ctx context.Context, d time.Duration) error {
		slept = append(slept, d)
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := retryWithBackoff(
		ctx,
		3,
		[]time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		func() error {
			attempts++
			return errors.New("send failed")
		},
		func(attempt int, total int, nextDelay time.Duration, err error) {
			if attempt == 1 {
				cancel()
			}
		},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, attempts)
	require.Equal(t, []time.Duration{time.Second}, slept)
}
