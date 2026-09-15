package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/stretchr/testify/require"
)

func TestGetClientCloseIdleConnections(t *testing.T) {
	originalValidate := validateResolvedIP
	t.Cleanup(func() { validateResolvedIP = originalValidate })
	validateCalls := 0
	validateResolvedIP = func(host string) error {
		require.Equal(t, "127.0.0.1", host)
		validateCalls++
		return nil
	}

	for _, validate := range []bool{false, true} {
		t.Run(fmt.Sprintf("validate=%t", validate), func(t *testing.T) {
			closed := make(chan struct{}, 4)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "complete response")
			}))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateClosed {
					closed <- struct{}{}
				}
			}
			server.Start()
			t.Cleanup(server.Close)

			opts := Options{Timeout: 5 * time.Second, ValidateResolvedIP: validate}
			client, err := GetClient(opts)
			require.NoError(t, err)
			t.Cleanup(func() {
				client.CloseIdleConnections()
				sharedClients.Delete(buildClientKey(opts))
			})
			get := func() bool {
				collector := servertiming.New(time.Now())
				ctx := servertiming.WithCollector(context.Background(), collector)
				var reused bool
				ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
					reused = info.Reused
				}})
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
				require.NoError(t, err)
				resp, err := client.Do(req)
				require.NoError(t, err)
				body, readErr := io.ReadAll(resp.Body)
				closeErr := resp.Body.Close()
				require.NoError(t, readErr)
				require.NoError(t, closeErr)
				require.Equal(t, "complete response", string(body))
				require.Contains(t, collector.HeaderValue(time.Now(), "bypass"), "dep_http;dur=")
				return reused
			}

			require.False(t, get())
			require.True(t, get(), "the response must leave a reusable idle connection")
			client.CloseIdleConnections()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("CloseIdleConnections did not close the real idle TCP connection")
			}
			cached, err := GetClient(opts)
			require.NoError(t, err)
			require.Same(t, client, cached, "closing idle connections must not evict the shared client")
			require.False(t, get(), "the next request must establish a new connection")
		})
	}
	require.Equal(t, 1, validateCalls, "IP validation and its TTL cache must remain active")
}

func TestGetClientCloseIdleConnectionsKeepsActiveResponse(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "start:")
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "finished")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	opts := Options{Timeout: 5 * time.Second}
	client, err := GetClient(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.CloseIdleConnections()
		sharedClients.Delete(buildClientKey(opts))
	})
	resp, err := client.Get(server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	client.CloseIdleConnections()
	unblock()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "start:finished", string(body))
}

func TestPoolReviewStableConfigurationAndFailures(t *testing.T) {
	opts := Options{Timeout: 7 * time.Second}
	client, err := GetClient(opts)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.CloseIdleConnections()
		sharedClients.Delete(buildClientKey(opts))
	})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 32 {
				cached, err := GetClient(opts)
				if err != nil || cached != client {
					t.Error("identical options did not return the same client")
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, bad := range []Options{
		{Timeout: opts.Timeout, ProxyURL: "ftp://proxy.invalid:1234"},
		{Timeout: opts.Timeout, InsecureSkipVerify: true},
	} {
		for range 2 {
			failed, err := GetClient(bad)
			require.Error(t, err)
			require.Nil(t, failed)
			_, exists := sharedClients.Load(buildClientKey(bad))
			require.False(t, exists, "a failed construction must not populate the pool")
		}
	}
	cached, err := GetClient(opts)
	require.NoError(t, err)
	require.Same(t, client, cached)
}

func TestPoolReviewValidationIsScopedAndFailureIsNotCached(t *testing.T) {
	originalValidate := validateResolvedIP
	t.Cleanup(func() { validateResolvedIP = originalValidate })
	calls := make(map[string]int)
	blocked := true
	rejected := errors.New("test host rejected")
	validateResolvedIP = func(host string) error {
		calls[host]++
		if host == "blocked.invalid" && blocked {
			return rejected
		}
		return nil
	}
	baseCalls := 0
	transport := newValidatedTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		baseCalls++
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
	}))
	now := time.Now()
	transport.now = func() time.Time { return now }
	request := func(host string) error {
		req, err := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		if resp != nil {
			require.NoError(t, resp.Body.Close())
		}
		return err
	}
	require.NoError(t, request("ALLOWED.invalid"))
	require.NoError(t, request("allowed.invalid:8443"))
	require.Equal(t, 1, calls["allowed.invalid"])
	for range 2 {
		require.ErrorIs(t, request("blocked.invalid"), rejected)
	}
	require.Equal(t, 2, baseCalls, "another host's success must not bypass rejection")
	require.Equal(t, 2, calls["blocked.invalid"])
	blocked = false
	require.NoError(t, request("blocked.invalid"))
	require.Equal(t, 3, calls["blocked.invalid"])
	now = now.Add(validatedHostTTL)
	require.NoError(t, request("allowed.invalid"))
	require.Equal(t, 2, calls["allowed.invalid"])
}
