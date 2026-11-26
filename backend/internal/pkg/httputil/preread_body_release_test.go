package httputil

import (
	"bytes"
	"io"
	"net/http"
	"runtime"
	"testing"
	"time"
	"weak"
)

func newReleasableBody(size int) (*PrereadBody, weak.Pointer[byte]) {
	body := bytes.Repeat([]byte{'x'}, size)
	return NewPrereadBody(body), weak.Make(&body[0])
}

func requireCollected(t *testing.T, w weak.Pointer[byte], msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if w.Value() == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(msg)
}

// Release must drop the only reference this wrapper holds to the request body.
func TestPrereadBodyReleaseLetsTheBodyBeCollected(t *testing.T) {
	p, w := newReleasableBody(1 << 20)
	if w.Value() == nil {
		t.Fatal("body must be reachable through the wrapper before Release")
	}
	p.Release()
	requireCollected(t, w, "released PrereadBody still pins the request body")
	runtime.KeepAlive(p)
}

func TestPrereadBodyReleaseSemantics(t *testing.T) {
	p, _ := newReleasableBody(64)
	p.Release()
	p.Release() // idempotent
	if p.Bytes() != nil {
		t.Fatalf("Bytes after Release = %d bytes, want nil", len(p.Bytes()))
	}
	n, err := p.Read(make([]byte, 8))
	if n != 0 || err != io.EOF {
		t.Fatalf("Read after Release = (%d, %v), want (0, EOF)", n, err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close after Release: %v", err)
	}
	var nilBody *PrereadBody
	nilBody.Release() // must not panic
}

// A released wrapper reads as an empty body: ReadRequestBodyWithPrealloc returns an empty result and no error.
func TestReadRequestBodyAfterReleaseIsEmpty(t *testing.T) {
	p, _ := newReleasableBody(64)
	req := &http.Request{Body: p, ContentLength: 64, Header: make(http.Header)}
	got, err := ReadRequestBodyWithPrealloc(req)
	if err != nil || len(got) != 64 {
		t.Fatalf("before Release: len=%d err=%v", len(got), err)
	}
	p.Release()
	got, err = ReadRequestBodyWithPrealloc(req)
	if err != nil || len(got) != 0 {
		t.Fatalf("after Release: len=%d err=%v", len(got), err)
	}
}
