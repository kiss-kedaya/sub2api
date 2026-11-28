package service

// Opaque blobs in a request body (base64 screenshots and files, encrypted reasoning
// state, thinking signatures) are not text. The upstream bills an image as a few
// hundred to a couple of thousand tokens whatever its byte size, but a text tokenizer
// run over its base64 gives about 0.7 token per byte: a body carrying a few
// screenshots was estimated at 9 to 12 million input tokens while the same session
// completed at 170 to 230 thousand (production, 2026-11-06, v2.3.13, rolled back).
//
// The fast incomplete-input estimator in incomplete_request_usage.go folds every
// long run of base64 characters inside a JSON string inline during its single-pass
// scan using opaqueBlobMinRun and isOpaqueBlobByte, without allocating a stripped
// copy of the request body.

// opaqueBlobMinRun is the shortest run of base64 characters treated as a blob. No
// natural word, identifier or URL gets near it: punctuation, spaces and escapes end
// a run.
const opaqueBlobMinRun = 1024

func isOpaqueBlobByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '+' || c == '/' || c == '=' || c == '-' || c == '_'
}
