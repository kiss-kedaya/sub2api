package requestcapture

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSuccessfulCaptureNeverPublishedAndDeletedAtCompletion(t *testing.T) {
	m, store := testManager(t)
	target := task(t, m, "user", 1, true)
	s := m.Begin(Meta{UserID: 1, RequestID: "success"})
	s.ClientRequest([]byte("{\"input\":\"PRIVATE_SUCCESS\",\"file_data\":\"PRIVATE_MEDIA\"}"), "application/json", nil)
	require.Eventually(t, func() bool { return m.Stats().UsedBytes > 0 }, time.Second, time.Millisecond)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	store.mu.Lock()
	require.Empty(t, store.records)
	store.mu.Unlock()
	dirs, err := os.ReadDir(filepath.Join(m.dir, target.ID))
	require.NoError(t, err)
	require.Len(t, dirs, 1)
	id := dirs[0].Name()
	_, err = m.Record(context.Background(), target.ID, id)
	require.Error(t, err)
	_, _, err = m.OpenPart(context.Background(), target.ID, id, "000001-client_request.txt")
	require.Error(t, err)
	s.Finish(200)
	drain(t, m)
	require.Zero(t, m.Stats().UsedBytes)
	_, err = os.Stat(filepath.Join(m.dir, target.ID, id))
	require.True(t, os.IsNotExist(err))
	v, err := m.Task(context.Background(), target.ID)
	require.NoError(t, err)
	require.Zero(t, v.Requests)
	require.Zero(t, v.Bytes)
	require.Zero(t, v.Partial)
	store.mu.Lock()
	require.Empty(t, store.records)
	store.mu.Unlock()
}

func TestErrorRetentionClassifiesBusinessFailuresOnly(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		payload      string
		attemptError bool
		captureError bool
		want         bool
	}{
		{"http400", 400, "{}", false, false, true},
		{"http503", 503, "{}", false, false, true},
		{"pretty_error", 200, "{\n  \"error\": {\n    \"code\": \"upstream_error\"\n  }\n}", false, false, true},
		{"null_error", 200, "{\"error\":null,\"response\":{\"error\":null}}", false, false, false},
		{"false_error", 200, "{\"error\":false}", false, false, false},
		{"zero_error", 200, "{\"error\":0}", false, false, false},
		{"empty_error", 200, "{\"error\":\"\"}", false, false, false},
		{"transport_retry", 200, "{}", true, false, true},
		{"capture_limit", 200, "{}", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "user", 1, false)
			s := m.Begin(Meta{UserID: 1})
			s.ClientRequest([]byte("{}"), "application/json", nil)
			if tc.attemptError {
				n := s.BeginAttempt(3)
				s.AttemptResponse(n, 0, nil, fmt.Errorf("do not persist this transport detail"))
			}
			if tc.captureError {
				s.MarkPartial("buffer_limit")
			}
			st := s.NewStream("upstream_response", 0, 0, "application/json", nil)
			for _, b := range []byte(tc.payload) {
				_, _ = st.Write([]byte{b})
			}
			_ = st.Close()
			s.Finish(tc.status)
			drain(t, m)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			if tc.want {
				require.Len(t, rows, 1)
				require.True(t, rows[0].IsError)
				require.NotNil(t, rows[0].FinishedAt)
			} else {
				require.Empty(t, rows)
				require.Zero(t, m.Stats().UsedBytes)
			}
		})
	}
}

func TestUnconfirmedDataDeletedWhenStoppedOrDisabled(t *testing.T) {
	for _, mode := range []string{"stop", "disable", "expire", "capture_failure"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "user", 1, false)
			s := m.Begin(Meta{UserID: 1})
			s.ClientRequest([]byte("{\"input\":\"PRIVATE_PENDING\"}"), "application/json", nil)
			require.Eventually(t, func() bool { return m.Stats().UsedBytes > 0 }, time.Second, time.Millisecond)
			switch mode {
			case "stop":
				require.NoError(t, m.Stop(context.Background(), target.ID))
			case "disable":
				m.ApplyConfig(Config{false, 1024, 7})
			case "expire":
				m.mu.Lock()
				m.tasks[target.ID].task.ExpiresAt = time.Now().Add(-time.Minute)
				m.mu.Unlock()
				m.signal()
			case "capture_failure":
				s.failCapture("queue_full")
				m.signal()
			}
			drain(t, m)
			require.Zero(t, m.Stats().UsedBytes)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Empty(t, rows)
			s.Finish(200) // Stopping capture never closes the business request.
		})
	}
}

func TestWebSocketSuccessErrorSuccessIsolatedBeforeDisconnect(t *testing.T) {
	for _, failTurn := range []int{1, 2} {
		t.Run(fmt.Sprint(failTurn), func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "user", 1, true)
			s := m.Begin(Meta{UserID: 1, Protocol: "websocket"})
			// Keep the writer behind the forwarding goroutine to exercise immutable turn snapshots.
			m.storageMu.Lock()
			for turn := 1; turn <= 3; turn++ {
				body := []byte(fmt.Sprintf("{\"type\":\"response.create\",\"model\":\"turn-%d\",\"input\":\"PRIVATE_TURN_%d\"}", turn, turn))
				s.ClientFrame(body)
				n := s.WSRequest(int64(turn), body)
				terminal := []byte("{\"type\":\"response.completed\",\"response\":{\"error\":null}}")
				if turn == failTurn {
					terminal = []byte("{\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"fixture\"}}}")
				}
				s.Frame("upstream_response", n, terminal)
				s.Frame("client_response", 0, terminal)
			}
			m.storageMu.Unlock()
			require.Eventually(t, func() bool { return s.pending.Load() == 0 }, 5*time.Second, time.Millisecond)
			require.Equal(t, 1, m.Stats().ActiveRequests) // No disconnect is necessary for deletion.
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			r := rows[0]
			require.Equal(t, failTurn, r.Turn)
			require.Equal(t, fmt.Sprintf("turn-%d", failTurn), r.Model)
			require.Len(t, r.Attempts, 1)
			require.EqualValues(t, failTurn, r.Attempts[0].AccountID)
			body := partsText(t, m, r)
			for turn := 1; turn <= 3; turn++ {
				if turn == failTurn {
					require.Contains(t, body, fmt.Sprintf("PRIVATE_TURN_%d", turn))
				} else {
					require.NotContains(t, body, fmt.Sprintf("PRIVATE_TURN_%d", turn))
				}
			}
			dirs, err := os.ReadDir(filepath.Join(m.dir, target.ID))
			require.NoError(t, err)
			require.Len(t, dirs, 1)
			require.Equal(t, r.Bytes, m.Stats().UsedBytes)
			s.Finish(http.StatusSwitchingProtocols)
			drain(t, m)
			rows, err = m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1)
		})
	}
}

func TestLegacyAndPendingRecordsCannotBeReadOrExported(t *testing.T) {
	m, store := testManager(t)
	target := task(t, m, "user", 1, false)
	now := time.Now().UTC()
	for _, r := range []Record{
		{ID: uuid.NewString(), TaskID: target.ID, InstanceID: m.instance, CreatedAt: now, FinishedAt: &now},
		{ID: uuid.NewString(), TaskID: target.ID, InstanceID: m.instance, CreatedAt: now, IsError: true},
	} {
		require.NoError(t, store.SaveRecord(context.Background(), &r))
		_, err := m.Record(context.Background(), target.ID, r.ID)
		require.ErrorIs(t, err, ErrNotFound)
		_, _, err = m.OpenPart(context.Background(), target.ID, r.ID, "any.txt")
		require.ErrorIs(t, err, ErrNotFound)
	}
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, m.Stop(context.Background(), target.ID))
	all, err := store.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	for _, r := range all {
		var denied bytes.Buffer
		require.ErrorIs(t, m.Export(context.Background(), &denied, target.ID, r.ID), ErrNotFound)
		require.Zero(t, denied.Len())
	}
	var archive bytes.Buffer
	require.NoError(t, m.Export(context.Background(), &archive, target.ID, ""))
}

func TestRestartPurgesSuccessPendingAndOrphanBodiesAcrossPages(t *testing.T) {
	store := newMemoryStore()
	dir := t.TempDir()
	m, err := New(store, dir, Config{true, 1024, 7})
	require.NoError(t, err)
	target := task(t, m, "user", 1, true)
	instance := m.instance
	m.Close()
	now := time.Now().UTC()
	// More than one page, mixed with retained errors, to detect deletion/offset skips.
	for i := 0; i < 210; i++ {
		r := Record{ID: uuid.NewString(), TaskID: target.ID, InstanceID: instance, CreatedAt: now.Add(time.Duration(i)), FinishedAt: &now, IsError: i%3 == 0, Bytes: 7}
		if i%3 == 1 {
			r.IsError = true
			r.FinishedAt = nil
		}
		require.NoError(t, store.SaveRecord(context.Background(), &r))
		path := filepath.Join(dir, target.ID, r.ID)
		require.NoError(t, os.MkdirAll(path, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(path, "body.txt"), []byte("private"), 0600))
	}
	orphan := filepath.Join(dir, target.ID, uuid.NewString())
	require.NoError(t, os.MkdirAll(orphan, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "body.txt"), []byte("orphan"), 0600))
	m, err = New(store, dir, Config{false, 1024, 7})
	require.NoError(t, err)
	defer m.Close()
	rows, err := store.Records(context.Background(), target.ID, "", false, 300, 0)
	require.NoError(t, err)
	require.Len(t, rows, 70)
	dirs, err := os.ReadDir(filepath.Join(dir, target.ID))
	require.NoError(t, err)
	require.Len(t, dirs, 70)
	require.EqualValues(t, 70*7, m.Stats().UsedBytes)
	v, err := m.Task(context.Background(), target.ID)
	require.NoError(t, err)
	require.EqualValues(t, 70, v.Requests)
	require.Equal(t, m.Stats().UsedBytes, v.Bytes)
}

func TestSSEErrorEventWithoutErrorTypeIsRetained(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	st := s.NewStream("upstream_response", 1, 0, "Text/Event-Stream; Charset=UTF-8", nil)
	for _, b := range []byte("event: error\ndata: {\"message\":\"upstream rejected\"}\n\n") {
		_, _ = st.Write([]byte{b})
	}
	_ = st.Close()
	s.Finish(200)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].IsError)
}

func TestGracefulShutdownDiscardsUnclassifiedBody(t *testing.T) {
	store := newMemoryStore()
	dir := t.TempDir()
	m, err := New(store, dir, Config{true, 1024, 7})
	require.NoError(t, err)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	s.ClientRequest([]byte("{\"input\":\"pending body\"}"), "application/json", nil)
	require.Eventually(t, func() bool { return m.Stats().UsedBytes > 0 }, time.Second, time.Millisecond)
	m.Close()
	rows, err := store.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, m.Stats().UsedBytes)
	dirs, err := os.ReadDir(filepath.Join(dir, target.ID))
	require.NoError(t, err)
	require.Empty(t, dirs)
}
