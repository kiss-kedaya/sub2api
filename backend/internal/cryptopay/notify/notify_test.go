package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWebhookSinkPostsStructuredEventAndBarkFields(t *testing.T) {
	type captured struct {
		body        []byte
		contentType string
	}
	received := make(chan captured, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- captured{body: body, contentType: r.Header.Get("Content-Type")}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	at := time.Date(2026, 11, 22, 10, 30, 0, 0, time.UTC)
	sink := NewWebhookSink()
	err := sink.Send(context.Background(), Config{
		Enabled:    true,
		WebhookURL: server.URL + "/push",
	}, Event{
		Event:   "sweep.destination_changed",
		At:      at,
		ActorID: 41,
		Detail:  map[string]any{"hot_wallet_evm": "0x2222222222222222222222222222222222222222"},
	})
	require.NoError(t, err)

	got := <-received
	require.Equal(t, "application/json", got.contentType)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(got.body, &payload))
	require.Equal(t, "sweep.destination_changed", payload["event"])
	require.Equal(t, at.Format(time.RFC3339), payload["at"])
	require.EqualValues(t, 41, payload["actor_user_id"])
	require.Equal(t, "[sub2api] 钱包操作：归集地址变更", payload["title"])
	require.Contains(t, payload["body"], "归集地址变更")
	require.Contains(t, payload["body"], "0x2222222222222222222222222222222222222222")
	detail, ok := payload["detail"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "0x2222222222222222222222222222222222222222", detail["hot_wallet_evm"])
}

func TestWebhookSinkReportsNon2xxAndUnsupportedScheme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	sink := NewWebhookSink()
	err := sink.Send(context.Background(), Config{Enabled: true, WebhookURL: server.URL}, Event{Event: "signer.setup"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 500")

	err = sink.Send(context.Background(), Config{Enabled: true, WebhookURL: "file:///tmp/notify"}, Event{Event: "signer.setup"})
	require.Error(t, err)

	// An empty endpoint means "channel not configured", not a failure.
	require.NoError(t, sink.Send(context.Background(), Config{Enabled: true}, Event{Event: "signer.setup"}))
}

type recordingEmailSender struct {
	mu      sync.Mutex
	sent    []string
	err     error
	subject string
	body    string
}

func (s *recordingEmailSender) SendEmail(_ context.Context, to, subject, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, to)
	s.subject = subject
	s.body = body
	return s.err
}

func TestEmailSinkUsesExistingMailSenderAndSkipsEmptyRecipient(t *testing.T) {
	sender := &recordingEmailSender{}
	sink := NewEmailSink(sender)

	require.NoError(t, sink.Send(context.Background(), Config{Enabled: true, Email: "ops@example.test"}, Event{
		Event: "signer.unlock", At: time.Now().UTC(), ActorID: 7, Detail: map[string]any{"source": "operator_share"},
	}))
	require.Equal(t, []string{"ops@example.test"}, sender.sent)
	require.Contains(t, sender.subject, "签名器解锁")
	require.Contains(t, sender.body, "operator_share")

	require.NoError(t, sink.Send(context.Background(), Config{Enabled: true}, Event{Event: "signer.unlock"}))
	require.Len(t, sender.sent, 1)

	// A missing sender must not panic.
	require.NoError(t, NewEmailSink(nil).Send(context.Background(), Config{Enabled: true, Email: "ops@example.test"}, Event{Event: "signer.unlock"}))
}

func TestDisabledConfigSendsNothing(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Count only this test's own event: a test in another package's process can
		// dial a just-closed port that this server happened to get (seen once in CI,
		// together with a "closed server answered" failure in the cryptopay tests).
		var got struct {
			Event string `json:"event"`
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got.Event == "signer.setup" {
			calls.Add(1)
		}
	}))
	defer server.Close()

	dispatcher := NewDispatcher(func(context.Context) (Config, error) {
		return Config{Enabled: false, WebhookURL: server.URL}, nil
	}, WithSinks(NewWebhookSink()))
	defer dispatcher.Close()

	dispatcher.Notify(context.Background(), Event{Event: "signer.setup"})
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, calls.Load())
}

func TestDispatcherNeverBlocksCallerEvenWhenReceiverHangs(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	dispatcher := NewDispatcher(func(context.Context) (Config, error) {
		return Config{Enabled: true, WebhookURL: server.URL}, nil
	}, WithSinks(NewWebhookSink()), WithTimeout(50*time.Millisecond))
	defer dispatcher.Close()

	// The caller passes an already-canceled context: delivery must still make
	// progress (it runs detached) and the call itself must return at once.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for range 100 {
		started := time.Now()
		dispatcher.Notify(canceled, Event{Event: "signer.verify_share"})
		require.Less(t, time.Since(started), 100*time.Millisecond)
	}
}

func TestDispatcherSurvivesProviderErrorAndServerFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	dispatcher := NewDispatcher(func(context.Context) (Config, error) {
		return Config{Enabled: true, WebhookURL: server.URL}, nil
	}, WithSinks(NewWebhookSink()), WithTimeout(2*time.Second))
	defer dispatcher.Close()

	for range 5 {
		dispatcher.Notify(context.Background(), Event{Event: "sweep.executed"})
	}
	require.Eventually(t, func() bool { return attempts.Load() >= 1 }, 2*time.Second, 20*time.Millisecond)
}

func TestDispatcherProviderErrorDoesNotPanic(t *testing.T) {
	dispatcher := NewDispatcher(func(context.Context) (Config, error) {
		return Config{}, context.DeadlineExceeded
	}, WithSinks(NewWebhookSink()))
	defer dispatcher.Close()

	dispatcher.Notify(context.Background(), Event{Event: "signer.setup"})
	time.Sleep(50 * time.Millisecond)
}

func TestBodyNeverInventsDetailsAndSortsKeys(t *testing.T) {
	body := Body(Event{
		Event:  "sweep.executed",
		At:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Detail: map[string]any{"b": "two", "a": 1},
	})
	require.Contains(t, body, "事件：sweep.executed")
	require.Contains(t, body, "操作人：系统")
	require.Less(t, indexOf(body, "a：1"), indexOf(body, "b：two"))
}

func indexOf(haystack, needle string) int {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return index
		}
	}
	return -1
}
