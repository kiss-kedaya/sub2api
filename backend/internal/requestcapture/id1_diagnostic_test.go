package requestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func diagnosticManager(t *testing.T) (*Manager, *memoryStore, *Task) {
	t.Helper()
	store := newMemoryStore()
	m, err := New(store, t.TempDir(), Config{QuotaMiB: 32, RetentionDays: 7, ID1DiagnosticEnabled: true})
	require.NoError(t, err)
	t.Cleanup(m.Close)
	tasks, err := m.Tasks(context.Background(), 20, 0)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, ID1DiagnosticMode, tasks[0].Mode)
	require.True(t, tasks[0].SaveMedia)
	return m, store, &tasks[0]
}

func TestID1DiagnosticCapturesSuccessFailureRetriesAndLargeBodiesOnlyForID1(t *testing.T) {
	m, _, task := diagnosticManager(t)
	require.False(t, m.Config().Enabled, "generic capture stays disabled")
	require.Nil(t, m.Begin(Meta{UserID: 2, ClientRequestID: "pretend-user-1"}))
	for _, status := range []int{200, 500} {
		s := m.Begin(Meta{UserID: 1, RequestID: uuid.NewString(), SessionID: "diagnostic-session", Protocol: "http"})
		require.NotNil(t, s)
		large := bytes.Repeat([]byte("x"), 600<<10)
		body := append([]byte(`{"model":"fixture","input":"`), large...)
		body = append(body, []byte(`","api_key":"PRIVATE-SECRET"}`)...)
		s.ClientRequest(body, "application/json", http.Header{"Authorization": []string{"SECRET-HEADER"}})
		first := s.BeginAttempt(10)
		s.AttemptResponse(first, 503, http.Header{"X-Request-Id": []string{"up-retry"}}, nil)
		for _, stage := range []string{"upstream_request", "upstream_response", "client_response"} {
			st := s.NewStream(stage, first, 0, "application/json", nil)
			_, err := st.Write([]byte(`{"output":"diagnostic","file_data":"MEDIA"}`))
			require.NoError(t, err)
			require.NoError(t, st.Close())
		}
		s.Finish(status)
		drain(t, m)
	}
	drain(t, m)
	rows, err := m.Records(context.Background(), task.ID, "", false, 20, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		r, err := m.Record(context.Background(), task.ID, row.ID)
		require.NoError(t, err)
		require.NotNil(t, r.ExpiresAt)
		require.WithinDuration(t, r.CreatedAt.Add(8*time.Hour), *r.ExpiresAt, time.Millisecond)
		content := partsText(t, m, *r)
		require.Contains(t, content, string(bytes.Repeat([]byte("x"), 600<<10)))
		require.Contains(t, content, "MEDIA")
		require.NotContains(t, content, "PRIVATE-SECRET")
		require.NotContains(t, content, "SECRET-HEADER")
		require.Equal(t, "up-retry", r.Attempts[0].UpstreamRequestID)
	}
	var archive bytes.Buffer
	require.NoError(t, m.Export(context.Background(), &archive, task.ID, rows[0].ID), "finalized single record can be exported without stopping continuous capture")
	require.ErrorIs(t, m.Export(context.Background(), io.Discard, task.ID, ""), ErrFinalizing)
}

func TestID1DiagnosticRetainsPlainSuccessAndWebSocketMultipleTurns(t *testing.T) {
	m, _, task := diagnosticManager(t)
	s := m.Begin(Meta{UserID: 1, RequestID: "plain", Protocol: "http"})
	s.ClientRequest([]byte(`{"input":"ok"}`), "application/json", nil)
	s.Finish(200)
	ws := m.Begin(Meta{UserID: 1, RequestID: "ws", Protocol: "websocket"})
	require.NotNil(t, ws)
	for i := 0; i < 2; i++ {
		ws.ClientFrame([]byte(`{"type":"response.create","model":"fixture","input":"turn"}`))
		n := ws.WSRequest(10, []byte(`{"type":"response.create","input":"tool result"}`))
		ws.AttemptResponse(n, 101, nil, nil)
		ws.Frame("upstream_response", n, []byte(`{"type":"response.completed","response":{"status":"completed"}}`))
		ws.Frame("client_response", 0, []byte(`{"type":"response.completed","response":{"status":"completed"}}`))
	}
	ws.Finish(101)
	drain(t, m)
	rows, err := m.Records(context.Background(), task.ID, "", false, 20, 0)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	for _, row := range rows {
		require.False(t, row.IsError)
	}
}

func TestID1DiagnosticRestartsResumesAndKeepsSuccessfulRecords(t *testing.T) {
	store := newMemoryStore()
	dir := t.TempDir()
	cfg := Config{QuotaMiB: 32, RetentionDays: 7, ID1DiagnosticEnabled: true}
	m, err := New(store, dir, cfg)
	require.NoError(t, err)
	tasks, err := m.Tasks(context.Background(), 20, 0)
	require.NoError(t, err)
	id := tasks[0].ID
	s := m.Begin(Meta{UserID: 1, Protocol: "http"})
	s.ClientRequest([]byte(`{"input":"success"}`), "application/json", nil)
	s.Finish(200)
	drain(t, m)
	store.mu.Lock()
	for recordID, raw := range store.records {
		var persisted Record
		require.NoError(t, json.Unmarshal(raw, &persisted))
		persisted.ExpiresAt = nil
		encoded, e := json.Marshal(persisted)
		require.NoError(t, e)
		store.records[recordID] = encoded
	}
	store.mu.Unlock()
	m.Close()
	m, err = New(store, dir, cfg)
	require.NoError(t, err)
	defer m.Close()
	tasks, err = m.Tasks(context.Background(), 20, 0)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, id, tasks[0].ID)
	require.Equal(t, "running", tasks[0].Status)
	rows, err := m.Records(context.Background(), id, "", false, 20, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].IsError)
	require.NotNil(t, rows[0].ExpiresAt, "restart must repair missing deadlines instead of retaining indefinitely")
	full, err := m.Record(context.Background(), id, rows[0].ID)
	require.NoError(t, err)
	require.Contains(t, partsText(t, m, *full), "success")
}

func TestID1DiagnosticExpiryDeletesActiveFilesAndMetadataWithoutRecreation(t *testing.T) {
	// Construct a worker-owned record directly so the precise clock boundary
	// can be advanced deterministically without sleeping eight hours.
	store := newMemoryStore()
	dir := t.TempDir()
	task := Task{ID: uuid.NewString(), InstanceID: "fixture", Mode: ID1DiagnosticMode, TargetType: "user", TargetID: 1, Status: "running"}
	require.NoError(t, store.SaveTask(context.Background(), &task))
	m := &Manager{store: store, dir: dir, instance: "fixture", config: Config{QuotaMiB: 32, RetentionDays: 7, ID1DiagnosticEnabled: true}, tasks: map[string]*runtimeTask{task.ID: {task: task}}, wake: make(chan struct{}, 1)}
	m.tasks[task.ID].active.Store(true)
	r := &recordState{Record: Record{ID: uuid.NewString(), TaskID: task.ID, InstanceID: "fixture", CreatedAt: time.Now().Add(-8 * time.Hour)}, parts: map[string]*partState{}}
	require.True(t, m.prepareDiagnosticRecord(r, m.tasks[task.ID]))
	p := &partState{part: Part{Name: "000001-client_request.txt"}}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, task.ID, r.ID), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, task.ID, r.ID, p.part.Name), []byte("old"), 0600))
	m.used.Store(3)
	require.NoError(t, store.SaveRecord(context.Background(), &r.Record))
	m.expireDue(*r.ExpiresAt)
	require.True(t, r.expired)
	require.ErrorContains(t, m.writePart(r, p, []byte("must not return")), "retention_expired")
	_, err := os.Stat(filepath.Join(dir, task.ID, r.ID))
	require.True(t, os.IsNotExist(err))
	_, err = store.Record(context.Background(), task.ID, r.ID)
	require.Error(t, err)
	require.EqualValues(t, 0, m.used.Load())
	require.EqualValues(t, 1, m.expiredRecords.Load())
}

func TestID1DiagnosticExpiredFinishReleasesUnprocessedPartBudget(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Second)
	m := &Manager{}
	p := &partState{charge: 24 << 10}
	m.buffer.Store(p.charge)
	r := &recordState{Record: Record{ExpiresAt: &past}, parts: map[string]*partState{"part": p}}
	m.finishRecord(r, &runtimeTask{}, &Record{})
	require.Zero(t, m.buffer.Load())
	require.Nil(t, r.parts)
}

func TestID1DiagnosticSwitchIsIndependentAndDefaultOff(t *testing.T) {
	store := newMemoryStore()
	m, err := New(store, t.TempDir(), Config{QuotaMiB: 8, RetentionDays: 7})
	require.NoError(t, err)
	defer m.Close()
	require.Nil(t, m.Begin(Meta{UserID: 1}))
	m.ApplyConfig(Config{QuotaMiB: 8, RetentionDays: 7, ID1DiagnosticEnabled: true})
	require.Eventually(t, func() bool {
		tasks, e := m.Tasks(context.Background(), 20, 0)
		return e == nil && len(tasks) == 1 && tasks[0].Status == "running"
	}, time.Second, 10*time.Millisecond)
	require.Nil(t, m.Begin(Meta{UserID: 2}))
	s := m.Begin(Meta{UserID: 1})
	require.NotNil(t, s)
	s.Finish(200)
	drain(t, m)
	m.ApplyConfig(Config{QuotaMiB: 8, RetentionDays: 7})
	require.Nil(t, m.Begin(Meta{UserID: 1}))
	tasks, err := m.Tasks(context.Background(), 20, 0)
	require.NoError(t, err)
	require.Equal(t, "stopped", tasks[0].Status)
}
