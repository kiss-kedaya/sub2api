package requestcapture

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mu      sync.Mutex
	tasks   map[string][]byte
	records map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{tasks: map[string][]byte{}, records: map[string][]byte{}}
}
func (s *memoryStore) SaveTask(_ context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(t)
	s.tasks[t.ID] = b
	return err
}
func (s *memoryStore) Task(_ context.Context, instance, id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.tasks[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var t Task
	err := json.Unmarshal(b, &t)
	if t.InstanceID != instance {
		return nil, sql.ErrNoRows
	}
	return &t, err
}
func (s *memoryStore) Tasks(_ context.Context, instance string, limit, offset int) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Task{}
	for _, b := range s.tasks {
		var t Task
		_ = json.Unmarshal(b, &t)
		if t.InstanceID == instance {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset >= len(out) {
		return []Task{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}
func (s *memoryStore) SaveRecord(_ context.Context, r *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[r.TaskID]; !ok {
		return sql.ErrNoRows
	}
	b, err := json.Marshal(r)
	s.records[r.ID] = b
	return err
}
func (s *memoryStore) Record(_ context.Context, task, id string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.records[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var r Record
	err := json.Unmarshal(b, &r)
	if r.TaskID != task {
		return nil, sql.ErrNoRows
	}
	return &r, err
}
func (s *memoryStore) Records(_ context.Context, task, rid string, errorsOnly bool, limit, offset int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Record{}
	for _, b := range s.records {
		var r Record
		_ = json.Unmarshal(b, &r)
		if r.TaskID == task && (rid == "" || r.RequestID == rid) && (!errorsOnly || (r.IsError && r.FinishedAt != nil)) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset >= len(out) {
		return []Record{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}
func (s *memoryStore) DeleteRecord(_ context.Context, task, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if raw, ok := s.records[id]; ok {
		var r Record
		_ = json.Unmarshal(raw, &r)
		if r.TaskID == task {
			delete(s.records, id)
		}
	}
	return nil
}
func (s *memoryStore) DeleteTask(_ context.Context, instance, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	for key, b := range s.records {
		var r Record
		_ = json.Unmarshal(b, &r)
		if r.TaskID == id {
			delete(s.records, key)
		}
	}
	return nil
}

func testManager(t *testing.T) (*Manager, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	m, err := New(store, t.TempDir(), Config{true, 1024, 7})
	require.NoError(t, err)
	t.Cleanup(m.Close)
	return m, store
}
func task(t *testing.T, m *Manager, kind string, id int64, media bool) *Task {
	t.Helper()
	v, err := m.Create(context.Background(), CreateTask{kind, id, 10, media}, kind)
	require.NoError(t, err)
	return v
}
func drain(t *testing.T, m *Manager) {
	t.Helper()
	require.Eventually(t, func() bool { return m.Stats().ActiveRequests == 0 }, 10*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1<<20, m.Stats().BufferBytes)
}
func partsText(t *testing.T, m *Manager, r Record) string {
	t.Helper()
	var out strings.Builder
	for _, p := range r.Parts {
		if p.Bytes > 0 {
			file, _, err := m.OpenPart(context.Background(), r.TaskID, r.ID, p.Name)
			require.NoError(t, err)
			b, err := io.ReadAll(file)
			_ = file.Close()
			require.NoError(t, err)
			_, _ = out.Write(b)
		}
	}
	return out.String()
}

func TestFilterChunkBoundaries(t *testing.T) {
	input := `{"input":"normal conversation","api_key":"VERY-SECRET","nested":{"access_token":"ANOTHER-SECRET"},"image_url":"data:image/png;base64,aGVsbG8=","url":"https://name:password@example.com/image?token=URL-SECRET&width=10"}`
	for _, size := range []int{1, 3, 17, 32768} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newBodyFilter("application/json", false)
			var out bytes.Buffer
			for offset := 0; offset < len(input); offset += size {
				end := offset + size
				if end > len(input) {
					end = len(input)
				}
				_, _ = out.Write(f.Write([]byte(input[offset:end])))
			}
			tail, reason := f.End()
			_, _ = out.Write(tail)
			require.Equal(t, "media_metadata_only", reason)
			require.True(t, json.Valid(out.Bytes()), out.String())
			for _, secret := range []string{"VERY-SECRET", "ANOTHER-SECRET", "URL-SECRET", "aGVsbG8=", "name:password"} {
				require.NotContains(t, out.String(), secret)
			}
			require.Contains(t, out.String(), "normal conversation")
			require.Contains(t, out.String(), "sha256")
		})
	}
}
func TestFilterSecretContainersAndEscapedKeys(t *testing.T) {
	for _, input := range []string{`{"api\u005fkey":"SECRET","ok":1}`, `{"credentials":{"anything":"SECRET"},"ok":1}`, `{"authorization":["SECRET"],"ok":1}`} {
		f := newBodyFilter("application/json", true)
		var out []byte
		for _, b := range []byte(input) {
			out = append(out, f.Write([]byte{b})...)
		}
		require.NotContains(t, string(out), "SECRET")
		require.True(t, json.Valid(out), string(out))
	}
}
func TestFilterSSEAndMediaOptIn(t *testing.T) {
	input := "event: response.delta\ndata: {\"delta\":\"hello\",\"api_key\":\"SECRET\"}\n\ndata: [DONE]\n\n"
	f := newBodyFilter("text/event-stream", false)
	var out []byte
	for _, b := range []byte(input) {
		out = append(out, f.Write([]byte{b})...)
	}
	require.Contains(t, string(out), "event: response.delta")
	require.Contains(t, string(out), "hello")
	require.Contains(t, string(out), "[DONE]")
	require.NotContains(t, string(out), "SECRET")
	f = newBodyFilter("application/json", true)
	out = f.Write([]byte(`{"file_data":"BASE64","api_key":"SECRET"}`))
	require.Contains(t, string(out), "BASE64")
	require.NotContains(t, string(out), "SECRET")
}
func TestTargetAccountAndOverlappingTasks(t *testing.T) {
	m, _ := testManager(t)
	user := task(t, m, "user", 1, false)
	account := task(t, m, "account", 42, true)
	group := task(t, m, "group", 2, false)
	s := m.Begin(Meta{RequestID: "client-id", UserID: 1, GroupID: 2, Path: "/v1/responses", Protocol: "http"})
	require.NotNil(t, s)
	s.ClientRequest([]byte(`{"model":"model-a","input":"hello","file_data":"MEDIA","api_key":"SECRET"}`), "application/json", nil)
	a := s.BeginAttempt(9)
	st := s.NewStream("upstream_request", a, 0, "application/json", nil)
	_, _ = st.Write([]byte(`{"input":"OTHER-ACCOUNT"}`))
	_ = st.Close()
	s.AttemptResponse(a, 429, nil, nil)
	a = s.BeginAttempt(42)
	st = s.NewStream("upstream_request", a, 0, "application/json", nil)
	_, _ = st.Write([]byte(`{"input":"TARGET-ACCOUNT"}`))
	_ = st.Close()
	st = s.NewStream("upstream_response", a, 0, "application/json", nil)
	_, _ = st.Write([]byte(`{"output":"ok"}`))
	_ = st.Close()
	s.SetRoutedGroup(99)
	st = s.NewStream("client_response", 0, 0, "application/json", nil)
	_, _ = st.Write([]byte(`{"output":"ok"}`))
	_ = st.Close()
	s.Finish(200)
	drain(t, m)
	for _, target := range []*Task{user, account, group} {
		rs, err := m.Records(context.Background(), target.ID, "", false, 20, 0)
		require.NoError(t, err)
		require.Len(t, rs, 1)
		r := rs[0]
		require.False(t, r.Partial, r.Reason)
		require.EqualValues(t, 99, r.RoutedGroupID)
		require.EqualValues(t, 2, r.GroupID)
		require.Len(t, r.Attempts, 2)
		text := partsText(t, m, r)
		require.NotContains(t, text, "SECRET")
		if target == account {
			require.NotContains(t, text, "OTHER-ACCOUNT")
			require.Contains(t, text, "TARGET-ACCOUNT")
			require.Contains(t, text, "MEDIA")
		} else {
			require.Contains(t, text, "OTHER-ACCOUNT")
			require.NotContains(t, text, `"MEDIA"`)
		}
	}
}
func TestUnmatchedAccountNeverWritesBody(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "account", 42, false)
	s := m.Begin(Meta{UserID: 1})
	require.NotNil(t, s)
	s.ClientRequest([]byte(`{"input":"PRIVATE"}`), "application/json", nil)
	s.BeginAttempt(5)
	s.Finish(200)
	drain(t, m)
	rs, err := m.Records(context.Background(), target.ID, "", false, 20, 0)
	require.NoError(t, err)
	require.Empty(t, rs)
	require.Zero(t, m.Stats().UsedBytes)
}
func TestDisableStopsCaptureWithoutBlockingBusiness(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	require.NotNil(t, s)
	s.ClientRequest([]byte(`{"input":"hello"}`), "application/json", nil)
	m.ApplyConfig(Config{false, 1024, 7})
	n, err := s.NewStream("client_response", 0, 0, "application/json", nil).Write([]byte("business still writes"))
	require.NoError(t, err)
	require.Equal(t, 21, n)
	s.Finish(200)
	drain(t, m)
	v, err := m.Task(context.Background(), target.ID)
	require.NoError(t, err)
	require.Equal(t, "feature_disabled", v.Reason)
	require.Nil(t, m.Begin(Meta{UserID: 1}))
}
func TestQuotaStopsAndPreservesFiles(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, true)
	m.ApplyConfig(Config{true, 1, 7})
	s := m.Begin(Meta{UserID: 1})
	require.NotNil(t, s)
	s.MarkError("upstream_failure")
	s.ClientRequest([]byte(`{"input":"`+strings.Repeat("x", 2<<20)+`"}`), "application/json", nil)
	s.Finish(502)
	drain(t, m)
	require.LessOrEqual(t, m.Stats().UsedBytes, int64(1<<20))
	require.Positive(t, m.Stats().UsedBytes)
	v, err := m.Task(context.Background(), target.ID)
	require.NoError(t, err)
	require.Equal(t, "quota_exceeded", v.Reason)
	require.NoError(t, m.Delete(context.Background(), target.ID))
	require.Zero(t, m.Stats().UsedBytes)
}
func TestRestartInterruptsAndRetentionDeletes(t *testing.T) {
	store := newMemoryStore()
	dir := t.TempDir()
	m, err := New(store, dir, Config{true, 1024, 7})
	require.NoError(t, err)
	target := task(t, m, "user", 1, false)
	m.Close()
	target.Status = "running"
	target.EndedAt = nil
	require.NoError(t, store.SaveTask(context.Background(), target))
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		r := Record{ID: uuid.NewString(), TaskID: target.ID, InstanceID: m.InstanceID(), CreatedAt: now, FinishedAt: &now, Bytes: int64((i + 1) * 10), Partial: i == 0, IsError: true}
		if i == 1 {
			r.FinishedAt = nil
		}
		require.NoError(t, store.SaveRecord(context.Background(), &r))
	}
	m, err = New(store, dir, Config{true, 1024, 7})
	require.NoError(t, err)
	defer m.Close()
	v, err := m.Task(context.Background(), target.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", v.Status)
	require.Equal(t, "server_restart", v.Reason)
	require.EqualValues(t, 2, v.Requests)
	require.EqualValues(t, 1, v.Partial)
	require.EqualValues(t, 40, v.Bytes)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	for _, r := range rows {
		require.NotNil(t, r.FinishedAt)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	v.EndedAt = &old
	require.NoError(t, store.SaveTask(context.Background(), v))
	m.cleanExpired()
	_, err = m.Task(context.Background(), target.ID)
	require.Error(t, err)
}
func TestExportAndPermissions(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	s.ClientRequest([]byte(`{"input":"hello"}`), "application/json", nil)
	s.Finish(500)
	drain(t, m)
	require.NoError(t, m.Stop(context.Background(), target.ID))
	var archive bytes.Buffer
	require.NoError(t, m.Export(context.Background(), &archive, target.ID, ""))
	require.Positive(t, archive.Len())
	err := filepath.WalkDir(m.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			require.Equal(t, os.FileMode(0700), info.Mode().Perm(), p)
		} else {
			require.Equal(t, os.FileMode(0600), info.Mode().Perm(), p)
		}
		return nil
	})
	require.NoError(t, err)
}

func TestCapture200Concurrent(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "group", 1, false)
	sessions := make([]*Session, 200)
	for i := range sessions {
		sessions[i] = m.Begin(Meta{UserID: int64(i + 1), GroupID: 1, RequestID: fmt.Sprint(i), Protocol: "http"})
		require.NotNil(t, sessions[i])
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			s.ClientRequest([]byte(`{"input":"stress","image_url":"data:image/png;base64,`+strings.Repeat("YQ==", 4096)+`"}`), "application/json", nil)
			a := s.BeginAttempt(42)
			up := s.NewStream("upstream_response", a, 0, "text/event-stream", nil)
			down := s.NewStream("client_response", 0, 0, "text/event-stream", nil)
			for i := 0; i < 30; i++ {
				body := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
				_, _ = up.Write(body)
				_, _ = down.Write(body)
				time.Sleep(time.Millisecond)
			}
			_ = up.Close()
			_ = down.Close()
			status := 200
			if s.meta.UserID%2 == 0 {
				status = 502
			}
			s.Finish(status)
		}(s)
	}
	wg.Wait()
	drain(t, m)
	runtime.ReadMemStats(&after)
	require.LessOrEqual(t, m.Stats().PeakBufferBytes, BufferLimit)
	rs, err := m.Records(context.Background(), target.ID, "", false, 1000, 0)
	require.NoError(t, err)
	t.Logf("stored_requests=%d stats=%+v", len(rs), m.Stats())
	require.Len(t, rs, 100)
	for _, r := range rs {
		require.True(t, r.IsError)
		require.Zero(t, r.UserID%2)
	}
	partial := 0
	for _, r := range rs {
		if r.Partial {
			partial++
		}
	}
	t.Logf("concurrency=200 elapsed=%s requests=%d partial=%d peak_capture_buffer=%d retained_bytes=%d heap_before=%d heap_after=%d goroutines=%d", time.Since(started), len(rs), partial, m.Stats().PeakBufferBytes, m.Stats().UsedBytes, before.HeapAlloc, after.HeapAlloc, runtime.NumGoroutine())
}
