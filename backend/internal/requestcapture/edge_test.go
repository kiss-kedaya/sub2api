package requestcapture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEscapedURLsAndMediaContext(t *testing.T) {
	input := `{"url":"https:\/\/user:password@example.org\/image?api_key=SECRET","image":"\u0064ata:image/png;base64,HIDDEN","data":"ordinary tool result","source":{"data":"BASE64","media_type":"image/png"},"inlineData":{"data":"BASE64"}}`
	for _, size := range []int{1, 2, 13, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newBodyFilter("application/json", false)
			var out bytes.Buffer
			for start := 0; start < len(input); start += size {
				end := min(start+size, len(input))
				_, _ = out.Write(f.Write([]byte(input[start:end])))
			}
			tail, reason := f.End()
			_, _ = out.Write(tail)
			require.Equal(t, "media_metadata_only", reason)
			require.True(t, json.Valid(out.Bytes()), out.String())
			for _, secret := range []string{"SECRET", "HIDDEN", "BASE64", "password"} {
				require.NotContains(t, out.String(), secret)
			}
			require.Contains(t, out.String(), "ordinary tool result")
		})
	}
}
func TestMalformedInputStopsBeforeUnsafeContent(t *testing.T) {
	for _, input := range []string{`{"safe":1,"api_key":"SECRET`, `{"nested":[]} "api_key": "SECRET"`, `{"safe":1, api_key:SECRET}`, `{"api_key":{"a":["SECRET"]}}`} {
		f := newBodyFilter("", false)
		out := f.Write([]byte(input))
		tail, _ := f.End()
		out = append(out, tail...)
		require.NotContains(t, string(out), "SECRET")
	}
	f := newBodyFilter("text/event-stream", false)
	f.Write([]byte("data: {\"api_key\":\"SECRET\n\ndata: {\"ok\":1}\n\n"))
	_, reason := f.End()
	require.Equal(t, "invalid_or_truncated_content", reason)
}
func TestResultMetadataAcrossBoundaries(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	body := []byte("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"upstream_failure\"},\"usage\":{\"input_tokens\":13}}}\n\n")
	s.ClientRequest([]byte(`{"model":"test"}`), "application/json", nil)
	st := s.NewStream("client_response", 0, 0, "text/event-stream", nil)
	for _, b := range body {
		_, _ = st.Write([]byte{b})
		s.ObserveResult([]byte{b})
	}
	_ = st.Close()
	s.Finish(200)
	drain(t, m)
	records, err := m.Records(context.Background(), target.ID, "", true, 100, 0)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.True(t, records[0].IsError)
	require.Equal(t, "upstream_failure", records[0].ErrorCode)
	require.EqualValues(t, 13, records[0].Usage["input_tokens"])
}
func TestHTTPFourLegsPreserveBusinessBytes(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "group", 7, false)
	upstreamBody := `{"model":"upstream-model","api_key":"BODY-SECRET","input":"converted"}`
	upstreamResult := "data: {\"type\":\"response.failed\",\"usage\":{\"input_tokens\":2}}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil || string(b) != upstreamBody {
			t.Error("capture changed upstream payload", err)
		}
		if r.Header.Get("Authorization") != "Bearer HEADER-SECRET" {
			t.Error("capture changed auth")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-ID", "upstream-id")
		_, _ = io.WriteString(w, upstreamResult)
	}))
	defer upstream.Close()
	s := m.Begin(Meta{UserID: 1, GroupID: 7, RequestID: "local-id"})
	s.ClientRequest([]byte(`{"model":"client-model","input":"original"}`), "application/json", http.Header{"Authorization": {"Bearer HEADER-SECRET"}})
	s.SetRoutedGroup(9)
	req, err := http.NewRequest(http.MethodPost, upstream.URL, strings.NewReader(upstreamBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer HEADER-SECRET")
	_, done := s.ObserveHTTPRequest(req, 42)
	resp, err := upstream.Client().Do(req)
	done(resp, err)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, upstreamResult, string(b))
	output := s.NewStream("client_response", 0, 0, "application/json", nil)
	_, _ = output.Write([]byte(`{"output":"client converted","usage":{"input_tokens":2}}`))
	_ = output.Close()
	s.Finish(200)
	drain(t, m)
	records, err := m.Records(context.Background(), target.ID, "local-id", false, 100, 0)
	require.NoError(t, err)
	require.Len(t, records, 1)
	r := records[0]
	require.Len(t, r.Parts, 4)
	require.False(t, r.Partial, r.Reason)
	require.EqualValues(t, 9, r.RoutedGroupID)
	require.Equal(t, "upstream-id", r.Attempts[0].UpstreamRequestID)
	text := partsText(t, m, r)
	require.Contains(t, text, "original")
	require.Contains(t, text, "client converted")
	require.NotContains(t, text, "SECRET")
}
func TestWebSocketMultiTurnAndAccountSwitch(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "account", 2, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "websocket"})
	s.ClientFrame([]byte(`{"type":"response.create","model":"first"}`))
	s.WSRequest(1, []byte(`{"type":"response.create","input":"OTHER_ACCOUNT"}`))
	s.Frame("upstream_response", 1, []byte(`{"error":{"code":"retry"}}`))
	s.WSRequest(2, []byte(`{"type":"response.create","input":"MATCHED"}`))
	s.Frame("upstream_response", 2, []byte(`{"type":"response.completed"}`))
	s.Frame("client_response", 0, []byte(`{"type":"response.completed"}`))
	s.ClientFrame([]byte(`{"type":"response.create","model":"second"}`))
	s.WSRequest(2, map[string]any{"type": "response.create", "input": "next"})
	s.Frame("upstream_response", 1, []byte(`{"type":"response.completed"}`))
	s.Frame("client_response", 0, []byte(`{"type":"response.completed"}`))
	s.Finish(101)
	drain(t, m)
	records, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, records, 1)
	text := partsText(t, m, records[0])
	require.NotContains(t, text, "OTHER_ACCOUNT")
	require.Equal(t, 1, strings.Count(text, `"first"`))
	require.NotContains(t, text, "second")
	require.Len(t, records[0].Attempts, 2)
	require.False(t, records[0].Partial, records[0].Reason)
	turns := map[int]bool{}
	for _, p := range records[0].Parts {
		turns[p.Turn] = true
	}
	require.True(t, turns[1])
	require.False(t, turns[2])
}
func TestLimitsAndDiskFailureDoNotAffectTraffic(t *testing.T) {
	for _, mode := range []string{"buffer", "disk", "record"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := testManager(t)
			target := task(t, m, "user", 1, true)
			s := m.Begin(Meta{UserID: 1})
			if mode == "buffer" {
				require.True(t, m.reserve(BufferLimit-m.buffer.Load()))
				defer func() { m.buffer.Add(-(BufferLimit - (1 << 20) - (64 << 10))) }()
			}
			if mode == "disk" {
				require.NoError(t, os.WriteFile(filepath.Join(m.dir, target.ID), []byte("block directory"), 0600))
			}
			if mode == "record" {
				r := &recordState{Record: Record{TaskID: target.ID, ID: "record", Bytes: RecordLimit}}
				err := m.writePart(r, &partState{part: Part{Name: "test.txt"}}, []byte("x"))
				require.ErrorContains(t, err, "record_limit")
				s.Finish(200)
				return
			}
			st := s.NewStream("client_response", 0, 0, "application/json", nil)
			n, err := st.Write([]byte(`{"ok":true}`))
			require.NoError(t, err)
			require.Equal(t, 11, n)
			require.NoError(t, st.Close())
			s.Finish(200)
			require.Eventually(t, func() bool { return m.Stats().ActiveRequests == 0 }, 5*time.Second, 10*time.Millisecond)
			rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
			require.NoError(t, err)
			require.Empty(t, rows)
		})
	}
}
func TestDeleteDuringTrafficDoesNotResurrect(t *testing.T) {
	m, store := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.Frame("client_response", 0, []byte(`{"ok":true}`))
			time.Sleep(time.Millisecond)
		}
		s.Finish(200)
	}()
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, m.Delete(context.Background(), target.ID))
	wg.Wait()
	drain(t, m)
	_, err := store.Task(context.Background(), m.InstanceID(), target.ID)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(m.dir, target.ID))
	require.True(t, os.IsNotExist(err))
}
func TestPreviewPreservesUTF8AndRejectsTraversal(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, true)
	s := m.Begin(Meta{UserID: 1})
	s.ClientRequest([]byte(`{"text":"`+strings.Repeat("汉", 100000)+`"}`), "application/json", nil)
	s.Finish(400)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	r := rows[0]
	require.Len(t, r.Parts, 1)
	var out string
	offset := int64(0)
	for {
		text, next, more, err := m.Preview(context.Background(), target.ID, r.ID, r.Parts[0].Name, offset)
		require.NoError(t, err)
		out += text
		offset = next
		if !more {
			break
		}
	}
	require.Equal(t, 100000, strings.Count(out, "汉"))
	require.NotContains(t, out, "�")
	_, _, err = m.OpenPart(context.Background(), target.ID, r.ID, "../../.instance")
	require.Error(t, err)
}

func TestOverlappingTaskStopsWithoutWaitingForOtherTask(t *testing.T) {
	m, _ := testManager(t)
	one := task(t, m, "user", 1, false)
	two := task(t, m, "group", 2, false)
	s := m.Begin(Meta{UserID: 1, GroupID: 2})
	s.MarkError("upstream_failure")
	s.ClientRequest([]byte(`{"input":"first"}`), "application/json", nil)
	require.Eventually(t, func() bool { return m.Stats().UsedBytes > 0 }, 5*time.Second, time.Millisecond)
	require.NoError(t, m.Stop(context.Background(), one.ID))
	require.Eventually(t, func() bool { var out bytes.Buffer; return m.Export(context.Background(), &out, one.ID, "") == nil }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, 1, m.Stats().ActiveRequests)
	s.Frame("client_response", 0, []byte(`{"output":"after stop"}`))
	s.Finish(200)
	drain(t, m)
	first, _ := m.Records(context.Background(), one.ID, "", false, 10, 0)
	second, _ := m.Records(context.Background(), two.ID, "", false, 10, 0)
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	require.True(t, first[0].Partial)
	require.NotContains(t, partsText(t, m, first[0]), "after stop")
	require.Contains(t, partsText(t, m, second[0]), "after stop")
}
func TestBinaryMediaOptInAcrossChunks(t *testing.T) {
	f := newBodyFilter("image/png", true)
	var out []byte
	for _, p := range []string{"h", "ell", "o"} {
		out = append(out, f.Write([]byte(p))...)
	}
	tail, reason := f.End()
	out = append(out, tail...)
	require.Empty(t, reason)
	require.JSONEq(t, `{"encoding":"base64","data":"aGVsbG8="}`, string(out))
	f = newBodyFilter("image/png", false)
	require.Empty(t, f.Write([]byte("hello")))
	out, reason = f.End()
	require.Equal(t, "media_metadata_only", reason)
	require.Contains(t, string(out), "sha256")
	require.NotContains(t, string(out), "hello")
}

func TestWebSocketLongResponseUsesBoundedSegments(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "websocket"})
	s.ClientFrame([]byte(`{"type":"response.create"}`))
	s.WSRequest(2, []byte(`{"type":"response.create"}`))
	for i := 0; i < 1000; i++ {
		s.Frame("upstream_response", 1, []byte(`{"type":"response.output_text.delta","delta":"test"}`))
		s.Frame("client_response", 0, []byte(`{"type":"response.output_text.delta","delta":"test"}`))
	}
	s.Frame("client_response", 0, []byte(`{"type":"response.failed","error":{"code":"fixture"}}`))
	s.Finish(101)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Len(t, rows[0].Parts, 4)
	require.False(t, rows[0].Partial, rows[0].Reason)
	require.Equal(t, 2000, strings.Count(partsText(t, m, rows[0]), "response.output_text.delta"))
}

func TestWebSocketCompletedTurnReleasesFrameBuffers(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "websocket"})
	for turn := 1; turn <= 8; turn++ {
		s.ClientFrame([]byte(`{"type":"response.create"}`))
		n := s.WSRequest(2, []byte(`{"type":"response.create"}`))
		s.Frame("upstream_response", n, []byte(`{"type":"response.completed"}`))
		s.Frame("client_response", 0, []byte(`{"type":"response.completed"}`))
		s.frameMu.Lock()
		require.Empty(t, s.frameStreams)
		s.frameMu.Unlock()
		s.mu.Lock()
		require.Empty(t, s.streams)
		s.mu.Unlock()
	}
	s.Finish(101)
	drain(t, m)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, m.Stats().UsedBytes)
}

func TestFailedCaptureReleasesIdleBusinessConnection(t *testing.T) {
	m, _ := testManager(t)
	target := task(t, m, "user", 1, false)
	s := m.Begin(Meta{UserID: 1, Protocol: "websocket"})
	s.ClientFrame([]byte(`{"type":"response.create"}`))
	s.Frame("client_response", 0, []byte(`{"delta":"unfinished"}`))
	s.failCapture("queue_full")
	require.Eventually(t, func() bool { return m.Stats().ActiveRequests == 0 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, int64(1<<20), m.Stats().BufferBytes)
	rows, err := m.Records(context.Background(), target.ID, "", false, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, m.Stats().UsedBytes)
	// The business connection may continue after capture has released its budget.
	s.Frame("client_response", 0, []byte(`{"delta":"later"}`))
	s.Finish(101)
	require.Equal(t, int64(1<<20), m.Stats().BufferBytes)
}

func TestCamelCaseCredentialsAndWebSocketURLs(t *testing.T) {
	input := `{"accessToken":"SECRET-1","refreshToken":"SECRET-2","awsSecretAccessKey":"SECRET-3","nested":{"clientSecret":{"value":"SECRET-4"}},"socket":"wss://user:SECRET-5@example.org/path?accessToken=SECRET-6","plainSocket":"ws://user:SECRET-7@example.org/?apiKey=SECRET-8","usage":{"input_tokens":7},"text":"keep business text"}`
	for _, size := range []int{1, 7, 32, 1000} {
		f := newBodyFilter("application/json", false)
		var out bytes.Buffer
		for start := 0; start < len(input); start += size {
			_, _ = out.Write(f.Write([]byte(input[start:min(start+size, len(input))])))
		}
		tail, reason := f.End()
		_, _ = out.Write(tail)
		require.Empty(t, reason)
		require.True(t, json.Valid(out.Bytes()), out.String())
		require.NotContains(t, out.String(), "SECRET")
		require.Contains(t, out.String(), "keep business text")
		require.Contains(t, out.String(), `"input_tokens":7`)
	}
}

func TestSSETerminalMarkerAndUnsupportedFields(t *testing.T) {
	for _, size := range []int{1, 2, 32} {
		f := newBodyFilter("text/event-stream", false)
		input := "data: [DONE]"
		var out bytes.Buffer
		for start := 0; start < len(input); start += size {
			_, _ = out.Write(f.Write([]byte(input[start:min(start+size, len(input))])))
		}
		tail, reason := f.End()
		_, _ = out.Write(tail)
		require.Empty(t, reason)
		require.Equal(t, input, out.String())
	}
	f := newBodyFilter("text/event-stream", false)
	out := f.Write([]byte("unknown: sensitive value\n\ndata: [DONE]\n\n"))
	tail, reason := f.End()
	out = append(out, tail...)
	require.Equal(t, "unsupported_sse_field", reason)
	require.NotContains(t, string(out), "sensitive value")
	require.Contains(t, string(out), "[DONE]")
}
