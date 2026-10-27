package requestcapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const completedCaptureEvent = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1}}}\n\n"

func TestBodyFramingDoesNotCopyLargeRead(t *testing.T) {
	for _, first := range []string{"", "da"} {
		f := newBodyFraming("")
		prefix, rest := f.detect([]byte(first))
		require.Empty(t, prefix)
		require.Empty(t, rest)
		input := []byte(strings.TrimPrefix("data:", first) + strings.Repeat("x", 1<<20))
		prefix, rest = f.detect(input)
		require.Equal(t, "data:", string(prefix))
		require.Len(t, rest, 1<<20)
		require.Same(t, &input[5-len(first)], &rest[0], "framing must not allocate a payload-sized copy")
		require.True(t, f.sse)
	}
}

func TestResponseObserverChunkedTerminals(t *testing.T) {
	large := strings.Repeat("x", 96<<10)
	cases := []struct {
		name, ct, body  string
		success, failed bool
	}{
		{"completed", "text/event-stream", completedCaptureEvent, true, false},
		{"missing_type", "", completedCaptureEvent, true, false},
		{"large_late_type", "", "data: {\"response\":{\"output\":[{\"text\":\"" + large + "\"}]},\"type\":\"response.completed\"}\n\n", true, false},
		{"large_failed", "text/event-stream", "data: {\"response\":{\"output\":[\"" + large + "\"]},\"type\":\"response.failed\"}\n\n", false, true},
		{"incomplete", "", "data: {\"type\":\"response.incomplete\"}\n\n", false, true},
		{"cancelled", "", "data: {\"type\":\"response.cancelled\"}\n\n", false, true},
		{"failure_then_done", "", "data: {\"type\":\"error\"}\n\ndata: [DONE]\n\n", false, true},
		{"done", "", "data: [DONE]\n\n", true, false},
		{"invalid_done", "", "data: [DONE]garbage\n\n", false, false},
		{"invalid_done_spaces", "", "data: [ D O N E ]\n\n", false, false},
		{"invalid_done_multiline", "", "data: [DO\ndata: NE]\n\n", false, false},
		{"multiline", "", "data: {\"type\":\ndata: \"response.completed\"}\n\n", true, false},
		{"comment_crlf_bom", "", "\xef\xbb\xbf: heartbeat\r\nevent: response.completed\r\ndata: {\"type\":\"response.completed\"}\r\n\r\n", true, false},
		{"escaped_key", "", "data: {\"\\u0074ype\":\"response.completed\"}\n\n", true, false},
		{"truncated", "text/event-stream", "event: response.completed\ndata: {\"type\":\"response.completed\"", false, false},
		{"nested_fake_terminal", "", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"response.completed\",\"status\":\"completed\"}}\n\n", false, false},
		{"header_only", "", "event: response.completed\n\n", false, false},
		{"json", "", "{\"text\":\"data: response.completed\"}", true, false},
		{"json_error", "", "{\"error\":{\"code\":\"failed\"}}", false, true},
		{"json_null_error", "", "{\"error\":null}", true, false},
		{"json_true_error", "", "{\"response\":{\"error\":true}}", false, true},
		{"json_nested_fake_error", "", "{\"output\":[{\"error\":{\"code\":\"example\"}}]}", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{1, 3, 17, 32768} {
				o := responseObserver{framing: newBodyFraming(tc.ct)}
				for start := 0; start < len(tc.body); start += size {
					o.write([]byte(tc.body[start:min(start+size, len(tc.body))]))
				}
				o.end()
				require.Equal(t, tc.success, o.successful(), "chunk size %d", size)
				require.Equal(t, tc.failed, o.failed, "chunk size %d", size)
			}
		})
	}
}

// Close cancels a pending Read, as the production HTTP transport does after
// forwarding the terminal event. No captured customer payload is used.
type cancelCaptureBody struct {
	reader  *strings.Reader
	reading chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *cancelCaptureBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if err == io.EOF {
		close(b.reading)
		<-b.closed
		return 0, context.Canceled
	}
	return n, err
}

func (b *cancelCaptureBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestCompletedCaptureLocalCloseDeletesSuccessfulBodies(t *testing.T) {
	for _, ct := range []string{"text/event-stream", ""} {
		for _, large := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/large=%v", ct, large), func(t *testing.T) {
				m, store := testManager(t)
				target := task(t, m, "user", 1, false)
				s := m.Begin(Meta{UserID: 1, Protocol: "http"})
				require.NotNil(t, s)
				s.ClientRequest([]byte("{\"input\":\"PRIVATE_SUCCESS\"}"), "application/json", nil)
				n := s.BeginAttempt(60)
				s.AttemptResponse(n, http.StatusOK, nil, nil)
				payload := completedCaptureEvent
				if large {
					payload = "data: {\"response\":{\"output\":[\"" + strings.Repeat("x", 96<<10) + "\"]},\"type\":\"response.completed\"}\n\n"
				}
				raw := &cancelCaptureBody{reader: strings.NewReader(payload), reading: make(chan struct{}), closed: make(chan struct{})}
				body := ObserveBody(raw, s.NewStream("upstream_response", n, 0, ct, nil))
				client := s.NewStream("client_response", 0, 0, "text/event-stream", nil)
				buf := make([]byte, len(payload))
				read, err := body.Read(buf)
				require.NoError(t, err)
				require.Equal(t, payload, string(buf[:read]), "business bytes must stay unchanged")
				_, err = client.Write(buf[:read])
				require.NoError(t, err)
				require.NoError(t, client.Close())
				require.Eventually(t, func() bool { return m.Stats().UsedBytes > 0 }, time.Second, time.Millisecond)
				readDone := make(chan error, 1)
				go func() { _, e := body.Read(make([]byte, 4096)); readDone <- e }()
				select {
				case <-raw.reading:
				case <-time.After(time.Second):
					t.Fatal("pending read did not start")
				}
				require.NoError(t, body.Close())
				select {
				case err = <-readDone:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(time.Second):
					t.Fatal("close did not unblock read")
				}
				s.Finish(200)
				drain(t, m)
				rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
				require.NoError(t, err)
				require.Empty(t, rows)
				require.Zero(t, m.Stats().UsedBytes)
				store.mu.Lock()
				require.Empty(t, store.records)
				store.mu.Unlock()
				entries, err := os.ReadDir(filepath.Join(m.dir, target.ID))
				if !os.IsNotExist(err) {
					require.NoError(t, err)
					require.Empty(t, entries)
				}
			})
		}
	}
}

type failingCaptureBody struct {
	reader *strings.Reader
	err    error
}

func (b *failingCaptureBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if err == io.EOF {
		return 0, b.err
	}
	return n, err
}
func (b *failingCaptureBody) Close() error { return nil }

func TestIncompleteCaptureRetainsSafeReadDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		class string
	}{
		{"cancel", context.Canceled, "context_canceled"},
		{"deadline", context.DeadlineExceeded, "deadline_exceeded"},
		{"truncated", io.ErrUnexpectedEOF, "unexpected_eof"},
		{"early_eof", io.EOF, "eof_before_terminal"},
		{"opaque", errors.New("https://user:PRIVATE_SECRET@example.test/?token=PRIVATE_SECRET"), "read_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "user", 1, false)
			s := m.Begin(Meta{UserID: 1})
			require.NotNil(t, s)
			s.ClientRequest([]byte("{}"), "application/json", nil)
			n := s.BeginAttempt(60)
			s.AttemptResponse(n, 200, nil, nil)
			payload := "data: {\"type\":\"response.in_progress\"}\n\n"
			body := ObserveBody(&failingCaptureBody{strings.NewReader(payload), tc.err}, s.NewStream("upstream_response", n, 0, "", nil))
			got, err := io.ReadAll(body)
			if tc.err == io.EOF {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.err)
			}
			require.Equal(t, payload, string(got))
			require.NoError(t, body.Close())
			s.Finish(200)
			drain(t, m)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.True(t, rows[0].IsError)
			require.Equal(t, "upstream_read_failed", rows[0].ErrorCode)
			require.Equal(t, tc.class, rows[0].Attempts[0].ReadError)
			require.False(t, rows[0].Attempts[0].LocalClose, "later cleanup must not replace the original read cause")
			require.NotContains(t, fmt.Sprintf("%+v", rows[0]), "PRIVATE_SECRET")
			require.Contains(t, partsText(t, m, rows[0]), "response.in_progress")
		})
	}
}

func TestUnknownContentTypeSSEPreservesRedaction(t *testing.T) {
	input := ": secret comment\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"api_key\":\"PRIVATE_SECRET\",\"image_url\":\"data:image/png;base64,aGVsbG8=\"}\n\n"
	for _, media := range []bool{false, true} {
		for _, size := range []int{1, 2, 7, 32768} {
			f := newBodyFilter("", media)
			var out strings.Builder
			for start := 0; start < len(input); start += size {
				_, _ = out.Write(f.Write([]byte(input[start:min(start+size, len(input))])))
			}
			tail, reason := f.End()
			_, _ = out.Write(tail)
			if media {
				require.Empty(t, reason)
			} else {
				require.Equal(t, "media_metadata_only", reason)
			}
			require.Contains(t, out.String(), "response.failed")
			require.NotContains(t, out.String(), "PRIVATE_SECRET")
			require.NotContains(t, out.String(), "secret comment")
			require.Equal(t, media, strings.Contains(out.String(), "aGVsbG8="))
		}
	}
}

func TestCaptureCompletionDoesNotEraseRealFailures(t *testing.T) {
	for _, mode := range []string{"previous_attempt", "same_attempt", "client_write"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "account", 60, false)
			s := m.Begin(Meta{UserID: 1})
			require.NotNil(t, s)
			s.ClientRequest([]byte("{}"), "application/json", nil)
			if mode == "previous_attempt" {
				n := s.BeginAttempt(60)
				s.AttemptResponse(n, 503, nil, errors.New("PRIVATE_TRANSPORT_DETAIL"))
			}
			n := s.BeginAttempt(60)
			s.AttemptResponse(n, 200, nil, nil)
			payload := completedCaptureEvent
			if mode == "same_attempt" {
				payload = "data: {\"type\":\"response.failed\"}\n\n" + payload
			}
			if mode == "client_write" {
				s.MarkError("client_write_failed")
			}
			body := ObserveBody(&failingCaptureBody{strings.NewReader(payload), context.Canceled}, s.NewStream("upstream_response", n, 0, "", nil))
			got, err := io.ReadAll(body)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, payload, string(got))
			require.NoError(t, body.Close())
			s.Finish(200)
			drain(t, m)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.True(t, rows[0].IsError)
			require.Equal(t, "response.completed", rows[0].Attempts[n-1].ResponseTerminal)
			require.NotContains(t, fmt.Sprintf("%+v", rows[0]), "PRIVATE_TRANSPORT_DETAIL")
			if mode == "client_write" {
				require.Equal(t, "client_write_failed", rows[0].ErrorCode)
			}
		})
	}
}

func TestCaptureCloseBeforeTerminalRemainsError(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	require.NotNil(t, s)
	s.ClientRequest([]byte("{}"), "application/json", nil)
	n := s.BeginAttempt(60)
	s.AttemptResponse(n, 200, nil, nil)
	body := ObserveBody(io.NopCloser(strings.NewReader("data: {\"type\":\"response.in_progress\"}\n\n")), s.NewStream("upstream_response", n, 0, "", nil))
	_, err := body.Read(make([]byte, 1024))
	require.NoError(t, err)
	require.NoError(t, body.Close())
	s.Finish(200)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "closed_before_terminal", rows[0].Attempts[0].ReadError)
	require.True(t, rows[0].Attempts[0].LocalClose)
}

func TestCaptureDoesNotRequireResponsesTerminalForOtherSSE(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	require.NotNil(t, s)
	s.ClientRequest([]byte("{}"), "application/json", nil)
	n := s.BeginAttempt(60)
	s.AttemptResponse(n, 200, nil, nil)
	payload := "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"
	body := ObserveBody(io.NopCloser(strings.NewReader(payload)), s.NewStream("upstream_response", n, 0, "text/event-stream", nil))
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, payload, string(got))
	require.NoError(t, body.Close())
	s.Finish(200)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
}
