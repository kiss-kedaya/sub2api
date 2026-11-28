package httputil

import (
	"bytes"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"
)

func TestDecompressRequestBody_PooledDecodersConcurrentReuseAndErrorRecovery(t *testing.T) {
	payloadA := []byte(strings.Repeat(`{"msg":"request-alpha"}`, 64))
	payloadB := []byte(strings.Repeat(`{"msg":"request-beta"}`, 64))

	for _, encoding := range []string{"zstd", "gzip", "deflate"} {
		t.Run(encoding, func(t *testing.T) {
			compA := compressAuditBody(t, encoding, payloadA)
			compB := compressAuditBody(t, encoding, payloadB)

			var wg sync.WaitGroup
			for g := 0; g < 8; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < 25; i++ {
						want := payloadA
						packed := compA
						if (id+i)%2 == 1 {
							want = payloadB
							packed = compB
						}
						got, err := decompressRequestBody(encoding, packed)
						if err != nil {
							t.Errorf("decompress %s failed: %v", encoding, err)
							return
						}
						if !bytes.Equal(got, want) {
							t.Errorf("decompress %s payload mismatch", encoding)
							return
						}
					}
				}(g)
			}
			wg.Wait()

			// Corrupt input must return an error without poisoning subsequent pooled reuse.
			if _, err := decompressRequestBody(encoding, []byte("not-valid-compressed-data")); err == nil {
				t.Fatalf("expected error for corrupt %s body", encoding)
			}
			got, err := decompressRequestBody(encoding, compA)
			if err != nil || !bytes.Equal(got, payloadA) {
				t.Fatalf("recovery after corrupt %s failed: err=%v len=%d", encoding, err, len(got))
			}
		})
	}
}

func TestDecompressRequestBody_ZstdConcurrencyOneDoesNotLeakGoroutines(t *testing.T) {
	packed := compressAuditBody(t, "zstd", []byte(strings.Repeat("pooled zstd ", 128)))
	for i := 0; i < 4; i++ {
		if _, err := decompressRequestBody("zstd", packed); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		if _, err := decompressRequestBody("zstd", packed); err != nil {
			t.Fatal(err)
		}
	}
	after := runtime.NumGoroutine()
	if after > before+4 {
		t.Fatalf("zstd pooled decoder leaked goroutines: before=%d after=%d", before, after)
	}
}

//go:noinline
func decompressAndDropRawSlice(t *testing.T, encoding string) weak.Pointer[byte] {
	t.Helper()
	packed := compressAuditBody(t, encoding, bytes.Repeat([]byte("a"), 32<<10))
	raw := append([]byte(nil), packed...)
	wp := weak.Make(&raw[0])
	if _, err := decompressRequestBody(encoding, raw); err != nil {
		t.Fatal(err)
	}
	return wp
}

func TestDecompressRequestBody_PooledDecodersReleaseRawSliceReference(t *testing.T) {
	for _, encoding := range []string{"zstd", "gzip", "deflate"} {
		t.Run(encoding, func(t *testing.T) {
			wp := decompressAndDropRawSlice(t, encoding)
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				runtime.GC()
				if wp.Value() == nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatalf("pooled %s decoder retained raw request bytes after release", encoding)
		})
	}
}
