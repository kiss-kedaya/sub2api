package requestcapture

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run separately so the ten-second soak never silently inflates every unit run.
func TestCapture200LongStreams(t *testing.T) {
	if os.Getenv("CAPTURE_LOAD_TEST") != "1" {
		t.Skip("set CAPTURE_LOAD_TEST=1 for the 200-stream soak")
	}
	m, _ := testManager(t)
	target := task(t, m, "user", 1, true)
	sessions := make([]*Session, 200)
	for i := range sessions {
		sessions[i] = m.Begin(Meta{UserID: 1, GroupID: int64(i % 2), RequestID: fmt.Sprint(i)})
		require.NotNil(t, sessions[i])
	}
	var before, peak, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	peak = before
	fds := func() int {
		for _, path := range []string{"/proc/self/fd", "/dev/fd"} {
			if files, err := os.ReadDir(path); err == nil {
				return len(files)
			}
		}
		return -1
	}
	fdBefore, fdPeak := fds(), fds()
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapAlloc > peak.HeapAlloc {
					peak = stats
				}
				if n := fds(); n > fdPeak {
					fdPeak = n
				}
			}
		}
	}()
	start := time.Now()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func(s *Session) {
			defer wg.Done()
			input := []byte(`{"model":"fixture","image_url":"data:image/png;base64,` + strings.Repeat("A", 32<<10) + `"}`)
			s.ClientRequest(input, "application/json", nil)
			n := s.WSRequest(2, input)
			upstream := s.NewStream("upstream_response", n, 0, "text/event-stream", nil)
			client := s.NewStream("client_response", 0, 0, "text/event-stream", nil)
			for i := 0; i < 200; i++ {
				event := []byte(fmt.Sprintf("data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture-%d\"}\n\n", i))
				_, _ = upstream.Write(event)
				_, _ = client.Write(event)
				time.Sleep(50 * time.Millisecond)
			}
			_, _ = upstream.Write([]byte("data: [DONE]\n\n"))
			_, _ = client.Write([]byte("data: [DONE]\n\n"))
			_ = upstream.Close()
			_ = client.Close()
			status := 200
			if s.meta.GroupID == 1 {
				status = 502
			}
			s.Finish(status)
		}(s)
	}
	wg.Wait()
	drain(t, m)
	close(done)
	<-sampled
	runtime.GC()
	runtime.ReadMemStats(&after)
	rows, err := m.Records(context.Background(), target.ID, "", false, 201, 0)
	require.NoError(t, err)
	require.Len(t, rows, 100)
	for _, r := range rows {
		require.True(t, r.IsError)
		require.EqualValues(t, 1, r.GroupID)
	}
	partial := 0
	for _, r := range rows {
		if r.Partial {
			partial++
		}
	}
	require.Zero(t, partial)
	require.LessOrEqual(t, m.Stats().PeakBufferBytes, BufferLimit)
	t.Logf("200 concurrent x 10s, 32KiB media input, two SSE legs x 200 events: elapsed=%s records=%d partial=%d payload_peak=%d disk_bytes=%d heap_before=%d heap_peak=%d heap_after_gc=%d fd_before=%d fd_peak=%d fd_after=%d", time.Since(start), len(rows), partial, m.Stats().PeakBufferBytes, m.Stats().UsedBytes, before.HeapAlloc, peak.HeapAlloc, after.HeapAlloc, fdBefore, fdPeak, fds())
}
