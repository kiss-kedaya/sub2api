package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func compressAuditBody(t testing.TB, encoding string, body []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	var writer io.WriteCloser
	var err error
	switch encoding {
	case "gzip":
		writer, err = gzip.NewWriterLevel(&output, gzip.BestSpeed)
	case "deflate":
		writer, err = zlib.NewWriterLevel(&output, zlib.BestSpeed)
	case "zstd":
		writer, err = zstd.NewWriter(&output, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	default:
		t.Fatalf("unsupported test encoding %q", encoding)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestCompressedBodyLimitPreservesBoundaryAndRejectsTruncation(t *testing.T) {
	body := bytes.Repeat([]byte{' '}, maxDecompressedBodySize+1)
	copy(body, `{"input":"original"}`)
	body[maxDecompressedBodySize] = 'x'
	for _, encoding := range []string{"gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			for _, size := range []int{maxDecompressedBodySize, maxDecompressedBodySize + 1} {
				t.Run(fmt.Sprint(size), func(t *testing.T) {
					packed := compressAuditBody(t, encoding, body[:size])
					req := newRequestWithBody(t, packed, encoding)
					got, err := ReadRequestBodyWithPrealloc(req)
					if size == maxDecompressedBodySize {
						if err != nil || !bytes.Equal(got, body[:size]) {
							t.Fatalf("exact boundary changed: bytes=%d err=%v", len(got), err)
						}
						return
					}
					var limitErr *http.MaxBytesError
					if !errors.As(err, &limitErr) || limitErr.Limit != maxDecompressedBodySize || got != nil {
						t.Fatalf("oversized compressed body was truncated: bytes=%d err=%v", len(got), err)
					}
					if req.Header.Get("Content-Encoding") != encoding || req.ContentLength != int64(len(packed)) {
						t.Fatal("failed decode changed request metadata")
					}
				})
			}
		})
	}
}

func TestCompressedBodyChecksumAtLimit(t *testing.T) {
	body := bytes.Repeat([]byte{' '}, maxDecompressedBodySize)
	copy(body, `{"input":"original"}`)
	for _, encoding := range []string{"gzip", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			packed := compressAuditBody(t, encoding, body)
			if encoding == "gzip" {
				packed[len(packed)-8] ^= 1
			} else {
				packed[len(packed)-1] ^= 1
			}
			got, err := ReadRequestBodyWithPrealloc(newRequestWithBody(t, packed, encoding))
			if err == nil || got != nil {
				t.Fatalf("corrupt stream accepted at exact boundary: bytes=%d err=%v", len(got), err)
			}
		})
	}
}

func BenchmarkDecompressRequestBody(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20} {
		body := bytes.Repeat([]byte{'x'}, size)
		packed := compressAuditBody(b, "gzip", body)
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				got, err := decompressRequestBody("gzip", packed)
				if err != nil || !bytes.Equal(got, body) {
					b.Fatalf("decompressed content changed: %v", err)
				}
			}
		})
	}
}
