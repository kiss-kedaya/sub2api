package requestcapture

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/tidwall/gjson"
)

const SmallCredentialPlaceholder = "<REPLACE_CREDENTIAL>"

type smallBlob struct {
	Hash, Codec string
	Payload     []byte
	RawSize     int
}
type preparedSmallExchange struct {
	SmallExchange
	Bucket                                                                   time.Time
	Fingerprint, ResponseFingerprint                                         string
	RequestHeaderBlob, RequestBodyBlob, ResponseHeaderBlob, ResponseBodyBlob smallBlob
	ReplayReasons                                                            []string
	RawBytes                                                                 int64
}

var smallVolatileHeaders = map[string]bool{
	"date": true, "x-request-id": true, "request-id": true, "x-client-request-id": true, "x-correlation-id": true,
	"x-amzn-requestid": true, "x-amz-request-id": true, "trace-id": true,
	"cf-ray": true, "traceparent": true, "tracestate": true, "x-amzn-trace-id": true,
	"content-length": true, "transfer-encoding": true, "connection": true, "keep-alive": true,
	"x-forwarded-for": true, "x-real-ip": true, "cf-connecting-ip": true,
	"server-timing": true, "x-runtime": true, "x-envoy-upstream-service-time": true,
}

func hashSmall(p []byte) string { sum := sha256.Sum256(p); return hex.EncodeToString(sum[:]) }
func smallCredentialName(name string, credentialHeader func(string) bool) bool {
	name = strings.ToLower(name)
	return (credentialHeader != nil && credentialHeader(name)) || sensitive(name) || strings.Contains(name, "authorization") || strings.Contains(name, "api-key") || strings.Contains(name, "api_key") || strings.Contains(name, "secret") || strings.Contains(name, "cookie") || name == "apikey"
}
func smallSafePath(path string, credentialHeader func(string) bool) string {
	u, err := url.ParseRequestURI(path)
	if err != nil {
		return "/"
	}
	query := u.Query()
	for k, values := range query {
		name := strings.ToLower(k)
		if smallCredentialName(name, credentialHeader) || name == "key" || strings.Contains(name, "signature") || name == "sig" || name == "credential" || strings.HasPrefix(name, "x-amz-") || strings.HasPrefix(name, "x-goog-") {
			for i := range values {
				values[i] = SmallCredentialPlaceholder
			}
			query[k] = values
		}
	}
	u.RawQuery, u.Fragment, u.User, u.Host, u.Scheme = query.Encode(), "", nil, "", ""
	return u.RequestURI()
}
func smallSafeHeaders(header http.Header, credentialHeader func(string) bool) http.Header {
	out := make(http.Header, len(header))
	for k, values := range header {
		name := strings.ToLower(k)
		clean := make([]string, len(values))
		for i, v := range values {
			switch {
			case smallCredentialName(name, credentialHeader):
				clean[i] = SmallCredentialPlaceholder
			case name == "referer" || name == "location" || name == "content-location":
				// These values can carry credential-bearing query strings too.
				if u, err := url.Parse(v); err == nil {
					safe := smallSafePath(u.RequestURI(), credentialHeader)
					u.RawQuery = ""
					if q := strings.IndexByte(safe, '?'); q >= 0 {
						u.RawQuery = safe[q+1:]
					}
					u.User = nil
					clean[i] = u.String()
				} else {
					clean[i] = SmallCredentialPlaceholder
				}
			default:
				clean[i] = v
			}
		}
		out[name] = clean
	}
	return out
}
func smallStableHeaders(header http.Header) []byte {
	stable := make(http.Header, len(header))
	for k, values := range header {
		if !smallVolatileHeaders[k] {
			stable[k] = values
		}
	}
	body, _ := json.Marshal(stable)
	return body
}
func makeSmallBlob(body []byte) smallBlob {
	b := smallBlob{Hash: hashSmall(body), Codec: "identity", Payload: body, RawSize: len(body)}
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write(body)
	_ = w.Close()
	if compressed.Len() < len(body) {
		b.Codec, b.Payload = "gzip", compressed.Bytes()
	}
	return b
}
func decodeSmallBlob(codec string, payload []byte) ([]byte, error) {
	if codec == "identity" {
		return payload, nil
	}
	if codec != "gzip" {
		return nil, fmt.Errorf("unsupported retention codec")
	}
	r, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(io.LimitReader(r, SmallResponseLimit+1))
	if len(body) > SmallResponseLimit {
		return nil, fmt.Errorf("retention blob too large")
	}
	return body, err
}
func inspectSmallBody(v any, reasons map[string]bool) bool {
	media := false
	switch value := v.(type) {
	case map[string]any:
		for k, child := range value {
			switch strings.ToLower(k) {
			case "previous_response_id", "file_id", "cachedcontent", "cached_content", "encrypted_content":
				if child != nil && child != "" {
					reasons["external_context:"+k] = true
				}
			case "image_url", "input_audio", "audio_url", "inline_data", "inlinedata", "filedata", "file_data":
				media = true
			case "type":
				if name, ok := child.(string); ok {
					switch name {
					case "image", "image_url", "input_image", "input_audio", "output_audio", "video", "input_video":
						media = true
					}
				}
			}
			if inspectSmallBody(child, reasons) {
				media = true
			}
		}
	case []any:
		for _, child := range value {
			if inspectSmallBody(child, reasons) {
				media = true
			}
		}
	}
	return media
}

// Explicit credential fields can be replaced without erasing opaque business
// keys, protocol reasoning signatures, or normal token/usage fields.
func smallBodyCredential(name string) bool {
	if name == "" {
		return false
	}
	name = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "-", ""), "_", ""))
	switch name {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "apikey", "xapikey", "xgoogapikey",
		"accesstoken", "refreshtoken", "idtoken", "authtoken", "bearertoken", "sessiontoken", "accesskey", "accesskeyid",
		"secretkey", "secretaccesskey", "awsaccesskeyid", "awssecretaccesskey", "clientsecret", "password", "privatekey", "credentials":
		return true
	default:
		return false
	}
}

func sanitizeSmallBody(body []byte, contentType string) (contents []byte, changed, ok bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, false, true
	}
	// In this worker the body is already bounded and decoded. Match the
	// payload when a relay mislabels a JSON response, without retaining raw
	// unrecognized response bodies that have no structured filtering boundary.
	if json.Valid(body) {
		contentType = "application/json"
	} else if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		contentType = ""
	}
	f := newBodyFilter(contentType, false)
	f.json.credential = smallBodyCredential
	contents = f.Write(body)
	end, reason := f.End()
	if reason != "" || !f.sse && !f.json.started {
		return nil, false, false
	}
	return append(contents, end...), f.redacted || f.json.redacted, true
}

func prepareSmallExchange(e SmallExchange, credentialHeader func(string) bool) (preparedSmallExchange, string) {
	p := preparedSmallExchange{SmallExchange: e, Bucket: e.StartedAt.UTC().Truncate(time.Hour)}
	decoded, err := decodeSmallResponse(e.ResponseBody, e.ResponseHeaders.Get("Content-Encoding"))
	if err != nil {
		return p, "unsupported_body"
	}
	if len(decoded) > SmallResponseLimit {
		return p, "response_too_large"
	}
	e.ResponseBody = decoded
	var body any
	if json.Unmarshal(e.RequestBody, &body) != nil {
		return p, "unsupported_body"
	}
	reasons := map[string]bool{}
	if inspectSmallBody(body, reasons) {
		return p, "unsupported_body"
	}
	request, requestRedacted, requestOK := sanitizeSmallBody(e.RequestBody, "application/json")
	response, _, responseOK := sanitizeSmallBody(e.ResponseBody, e.ResponseHeaders.Get("Content-Type"))
	if !requestOK || !responseOK {
		return p, "unsupported_body"
	}
	if len(request) > SmallRequestLimit {
		return p, "request_too_large"
	}
	if len(response) > SmallResponseLimit {
		return p, "response_too_large"
	}
	p.RequestBody, p.ResponseBody = request, response
	if requestRedacted {
		reasons["request_credentials_redacted"] = true
	}
	if e.Partial {
		reasons["client_delivery_incomplete"] = true
	}
	if strings.Contains(strings.ToLower(e.ResponseHeaders.Get("Content-Type")), "text/event-stream") && !smallSSETerminal(e.ResponseBody) {
		reasons["stream_terminal_missing"] = true
	}
	for reason := range reasons {
		p.ReplayReasons = append(p.ReplayReasons, reason)
	}
	sort.Strings(p.ReplayReasons)
	p.Path = smallSafePath(e.Path, credentialHeader)
	p.RequestHeaders = smallSafeHeaders(e.RequestHeaders, credentialHeader)
	p.ResponseHeaders = smallSafeHeaders(e.ResponseHeaders, credentialHeader)
	// The shared reader has already decompressed the request. Never reapply its
	// old transport framing when copying a replay command.
	p.RequestHeaders.Del("Content-Encoding")
	delete(p.RequestHeaders, "content-encoding")
	delete(p.ResponseHeaders, "content-encoding")
	p.RequestHeaderBlob, p.RequestBodyBlob = makeSmallBlob(smallStableHeaders(p.RequestHeaders)), makeSmallBlob(p.RequestBody)
	p.ResponseHeaderBlob, p.ResponseBodyBlob = makeSmallBlob(smallStableHeaders(p.ResponseHeaders)), makeSmallBlob(p.ResponseBody)
	fingerprint, _ := json.Marshal([]any{e.UserID, e.APIKeyID, e.Method, p.Path, e.Usage.Model, p.RequestHeaderBlob.Hash, p.RequestBodyBlob.Hash})
	p.Fingerprint = hashSmall(fingerprint)
	p.ResponseFingerprint = hashSmall([]byte(fmt.Sprintf("%d:%s:%s", e.Status, p.ResponseHeaderBlob.Hash, p.ResponseBodyBlob.Hash)))
	p.RawBytes = int64(p.RequestHeaderBlob.RawSize + p.RequestBodyBlob.RawSize + p.ResponseHeaderBlob.RawSize + p.ResponseBodyBlob.RawSize)
	return p, ""
}

func decodeSmallResponse(body []byte, encoding string) ([]byte, error) {
	var reader io.Reader = bytes.NewReader(body)
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return body, nil
	case "gzip", "x-gzip":
		r, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer func() { _ = r.Close() }()
		reader = r
	case "deflate":
		r, err := zlib.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer func() { _ = r.Close() }()
		reader = r
	case "br":
		reader = brotli.NewReader(reader)
	case "zstd":
		r, err := zstd.NewReader(reader, zstd.WithDecoderMaxMemory(SmallResponseLimit+1))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		reader = r
	default:
		return nil, fmt.Errorf("unsupported response encoding")
	}
	return io.ReadAll(io.LimitReader(reader, SmallResponseLimit+1))
}

func smallSSETerminal(body []byte) bool {
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			return true
		}
		root := gjson.ParseBytes(data)
		switch root.Get("type").String() {
		case "message_stop", "response.completed", "response.done", "response.failed", "response.incomplete", "error":
			return true
		}
		if root.Get("candidates.0.finishReason").String() != "" || root.Get("response.candidates.0.finishReason").String() != "" || root.Get("choices.0.finish_reason").Type == gjson.String || root.Get("error").Exists() {
			return true
		}
	}
	return false
}

// SmallCurl uses a synthetic origin and never resolves stored credentials or
// sends a request. Shell quoting prevents prompt text becoming shell commands.
func SmallCurl(method, path string, headers http.Header, body []byte) string {
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'" }
	command := "curl --request " + quote(method) + " " + quote("https://example.invalid"+path)
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if smallVolatileHeaders[strings.ToLower(k)] || strings.EqualFold(k, "host") || strings.EqualFold(k, "content-encoding") {
			continue
		}
		for _, v := range headers[k] {
			command += " \\\n  --header " + quote(k+": "+v)
		}
	}
	return command + " \\\n  --data-binary " + quote(string(body))
}
