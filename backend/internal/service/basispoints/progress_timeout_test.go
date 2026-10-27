package basispoints

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProgressTimeoutStopsSilenceAndUpstreamComments(t *testing.T) {
	for _, comments := range []bool{false, true} {
		r, w := io.Pipe()
		body := WithProgressTimeout(context.Background(), r, 50*time.Millisecond)
		done := make(chan struct{})
		stop := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { _ = w.Close() }()
			if !comments {
				<-stop
				return
			}
			for {
				if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
		started := time.Now()
		_, err := io.ReadAll(body)
		close(stop)
		_ = body.Close()
		if !errors.Is(err, ErrStreamProgressTimeout) || time.Since(started) > 500*time.Millisecond {
			t.Fatalf("progress deadline missed: %v", err)
		}
		if comments {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("comment writer leaked")
			}
		}
	}
}

func TestProgressTimeoutAllowsFragmentedDataAndDownstreamPause(t *testing.T) {
	r, w := io.Pipe()
	body := WithProgressTimeout(context.Background(), r, 70*time.Millisecond)
	defer func() { _ = body.Close() }()
	go func() {
		defer func() { _ = w.Close() }()
		for _, s := range []string{`data: {"type":`, `"response.output_text.delta",`, `"delta":"hello"}`, "\n\n"} {
			time.Sleep(25 * time.Millisecond)
			if _, err := io.WriteString(w, s); err != nil {
				return
			}
		}
	}()
	raw, err := io.ReadAll(body)
	if err != nil || !strings.Contains(string(raw), "hello") {
		t.Fatalf("fragmented data stalled: %s %v", raw, err)
	}
	body = WithProgressTimeout(context.Background(), io.NopCloser(strings.NewReader("data: first\ndata: second\n")), 30*time.Millisecond)
	defer func() { _ = body.Close() }()
	buf := make([]byte, 12)
	if _, err = body.Read(buf); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err = io.ReadAll(body); err != nil {
		t.Fatalf("downstream pause spent upstream budget: %v", err)
	}
}

func TestProgressTimeoutCancellationAndDisabledMode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	body := WithProgressTimeout(ctx, r, time.Second)
	defer func() { _ = body.Close() }()
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := io.ReadAll(body)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrStreamProgressTimeout) {
		t.Fatalf("cancellation misclassified: %v", err)
	}
	upstream := io.NopCloser(strings.NewReader("done"))
	if WithProgressTimeout(ctx, upstream, 0) != upstream {
		t.Fatal("disabled mode changed body")
	}
}
