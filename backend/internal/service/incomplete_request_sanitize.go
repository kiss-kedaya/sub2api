package service

// Opaque blobs in a request body (base64 screenshots and files, encrypted reasoning
// state, thinking signatures) are not text. The upstream bills an image as a few
// hundred to a couple of thousand tokens whatever its byte size, but a text tokenizer
// run over its base64 gives about 0.7 token per byte: a body carrying a few
// screenshots was estimated at 9 to 12 million input tokens while the same session
// completed at 170 to 230 thousand (production, 2026-11-06, v2.3.13, rolled back).
//
// stripOpaqueBlobs therefore folds every long run of base64 characters inside a JSON
// string into a short placeholder before an input estimate is made. It works on the
// raw bytes in one pass without decoding the body, so a multi-megabyte request costs
// one scan. Anything it folds is counted as roughly zero, which keeps the estimate a
// lower bound.

// opaqueBlobMinRun is the shortest run of base64 characters treated as a blob. No
// natural word, identifier or URL gets near it: punctuation, spaces and escapes end
// a run.
const opaqueBlobMinRun = 1024

const opaqueBlobPlaceholder = "[blob]"

func isOpaqueBlobByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '+' || c == '/' || c == '=' || c == '-' || c == '_'
}

// stripOpaqueBlobs returns body with each run of at least opaqueBlobMinRun base64
// characters inside a JSON string replaced by "[blob]". A data URI keeps its header
// (data:image/png;base64,) because only the payload is a run. A JSON-escaped slash
// (\/) belongs to a run, so PHP-style encoders are covered. Keys, numbers and
// structure are never touched (only string contents are), the result is valid JSON whenever the input was, and
// the input slice is returned as is when there is nothing to fold.
func stripOpaqueBlobs(body []byte) []byte {
	n := len(body)
	if n < opaqueBlobMinRun {
		return body
	}
	var out []byte
	copied := 0
	runStart := -1
	flush := func(end int) {
		if runStart >= 0 && end-runStart >= opaqueBlobMinRun {
			if out == nil {
				out = make([]byte, 0, 4096)
			}
			out = append(out, body[copied:runStart]...)
			out = append(out, opaqueBlobPlaceholder...)
			copied = end
		}
		runStart = -1
	}
	i := 0
	for i < n {
		if body[i] != '"' {
			i++
			continue
		}
		i++ // now inside a string
	scan:
		for i < n {
			switch c := body[i]; {
			case c == '"':
				flush(i)
				i++
				break scan
			case c == '\\':
				if i+1 < n && body[i+1] == '/' {
					if runStart < 0 {
						runStart = i
					}
					i += 2
					continue
				}
				flush(i)
				i += 2
			case isOpaqueBlobByte(c):
				if runStart < 0 {
					runStart = i
				}
				i++
			default:
				flush(i)
				i++
			}
		}
		end := i
		if end > n {
			end = n
		}
		flush(end) // an unterminated string still closes its run
	}
	if out == nil {
		return body
	}
	return append(out, body[copied:]...)
}
