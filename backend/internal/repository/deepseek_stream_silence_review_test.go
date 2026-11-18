package repository

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type silenceCompressedBody struct {
	prefix  *bytes.Reader
	closed  chan struct{}
	blocked chan struct{}
	once    sync.Once
	notify  sync.Once
	closes  atomic.Int32
}

func (b *silenceCompressedBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	b.notify.Do(func() { close(b.blocked) })
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *silenceCompressedBody) Close() error {
	b.once.Do(func() { b.closes.Add(1); close(b.closed) })
	return nil
}

func TestDeepSeekStreamSilenceCompressedCloseInterruptsRead(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			payload := []byte("data: synthetic output\n\n")
			var compressed []byte
			if encoding == "gzip" {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				_, err := w.Write(payload)
				require.NoError(t, err)
				require.NoError(t, w.Flush())
				compressed = append([]byte(nil), buf.Bytes()...)
				require.NoError(t, w.Close())
			} else {
				compressed = compressZstd(t, payload)
			}
			source := &silenceCompressedBody{prefix: bytes.NewReader(compressed), closed: make(chan struct{}), blocked: make(chan struct{})}
			resp := &http.Response{Header: http.Header{"Content-Encoding": {encoding}}, Body: source}
			decompressResponseBody(resp)
			readDone := make(chan error, 1)
			go func() { _, err := io.Copy(io.Discard, resp.Body); readDone <- err }()
			select {
			case <-source.blocked:
			case <-time.After(3 * time.Second):
				t.Fatal("decoder never blocked on input")
			}
			closeDone := make(chan error, 1)
			go func() { closeDone <- resp.Body.Close() }()
			select {
			case err := <-closeDone:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				_ = source.Close()
				t.Fatal("Close must interrupt input before draining decoder")
			}
			select {
			case err := <-readDone:
				require.Error(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("Read remained blocked")
			}
			require.NoError(t, resp.Body.Close())
			require.Equal(t, int32(1), source.closes.Load())
		})
	}
}
