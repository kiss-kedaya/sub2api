package httputil

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/stretchr/testify/require"
)

// refReadLenient is ReadLenientJSONRequestBodyWithPrealloc as it was before the replay pass was
// skipped when redundant: read (with the replay normalization), lenient normalization, replay again.
func refReadLenient(req *http.Request, maxNormalizedBytes int64) ([]byte, error) {
	body, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		return nil, err
	}
	body, err = NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
	if err != nil {
		return nil, err
	}
	if req != nil && req.URL != nil {
		body = ipnotice.NormalizeReplay(body, ipnotice.ReplayProtocolForPath(req.URL.Path))
	}
	return body, nil
}

func replayRequestBodies() map[string]string {
	notice := "NOTICE"
	fenced := func(user string) string {
		return `{"model":"m","messages":[{"role":"user","content":"` + user + `"},` +
			`{"role":"assistant","content":[{"type":"text","text":` + jsonQuote(ipnotice.WrapOutputText("answer", notice)) + `}]},` +
			`{"role":"user","content":"next"}]}`
	}
	return map[string]string{
		"plain":                 `{"model":"m","messages":[{"role":"user","content":"hi"}]}`,
		"raw-newline-in-string": "{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"line1\nline2\"}]}",
		"raw-tab-and-del":       "{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"a\tb\x7fc\"}]}",
		"bom":                   "\xef\xbb\xbf" + `{"model":"m","messages":[]}`,
		"bom-and-raw-control":   "\xef\xbb\xbf" + "{\"messages\":[{\"content\":\"a\nb\"}]}",
		"fenced-assistant":      fenced("hi"),
		// first replay sees invalid JSON (a raw tab inside a string) and changes nothing; the lenient
		// pass repairs the tab; the second replay is the one that has to do the work
		"fenced-assistant-with-raw-control": fenced("a\tb"),
		"bom-and-fenced":                    "\xef\xbb\xbf" + fenced("hi"),
		"msg-id-only-responses":             `{"input":[{"type":"item_reference","id":"msg_gateway_notice_1"},{"role":"user","content":"hi"}]}`,
		"msg-id-in-text":                    `{"messages":[{"role":"user","content":"see msg_gateway_notice_ in my notes"}]}`,
		"escaped-fence-in-text":             `{"messages":[{"role":"assistant","content":"` + `⁣⁣x⁣⁣` + `"}]}`,
		"not-json":                          `this is not json`,
		"empty":                             ``,
		"valid-json-no-object":              `[1,2,3]`,
	}
}

func jsonQuote(s string) string {
	var buf bytes.Buffer
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			buf.WriteByte('\\')
			buf.WriteRune(r)
		case '\n':
			buf.WriteString(`\n`)
		default:
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return buf.String()
}

// The lenient reader gives exactly what read + lenient + replay gave, whether or not the second
// replay pass is skipped: for every body, on every kind of path.
func TestReadLenientJSONRequestBodyMatchesReplayTwice(t *testing.T) {
	paths := []string{
		"/v1/messages", "/v1/messages/count_tokens", "/v1/responses", "/v1/chat/completions",
		"/v1beta/models/gemini-2.5-pro:generateContent", "/v1/models/x", "/v1/embeddings", "/",
	}
	rewrote := 0
	for name, body := range replayRequestBodies() {
		for _, path := range paths {
			want, wantErr := refReadLenient(httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body))), 1<<20)
			got, gotErr := ReadLenientJSONRequestBodyWithPrealloc(httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body))), 1<<20)
			require.Equal(t, wantErr == nil, gotErr == nil, "%s %s", name, path)
			require.Equal(t, string(want), string(got), "%s %s", name, path)
			if gotErr == nil && string(got) != body {
				rewrote++
			}
		}
	}
	require.Greater(t, rewrote, 12, "the matrix has to include bodies the replay or lenient passes rewrite")

	// the case the skip must not break: the replay pass only works once the lenient pass has repaired the body
	body := replayRequestBodies()["fenced-assistant-with-raw-control"]
	got, err := ReadLenientJSONRequestBodyWithPrealloc(httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(body))), 1<<20)
	require.NoError(t, err)
	require.NotContains(t, string(got), "⁣", "the notice fence is stripped after the control byte is repaired")
	require.Contains(t, string(got), `a\tb`, "and the raw tab ended up escaped")
}

// Requests with a request-less URL or a preread body take the same paths as before.
func TestReadLenientJSONRequestBodyPrereadAndNilURL(t *testing.T) {
	body := []byte(replayRequestBodies()["fenced-assistant"])
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", NewPrereadBody(body))
	want, err := refReadLenient(httptest.NewRequest(http.MethodPost, "/v1/messages", NewPrereadBody(body)), 1<<20)
	require.NoError(t, err)
	got, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1<<20)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))

	noURL := &http.Request{Body: http.NoBody}
	out, err := ReadLenientJSONRequestBodyWithPrealloc(noURL, 1<<20)
	require.NoError(t, err)
	require.Empty(t, out)
	out, err = ReadLenientJSONRequestBodyWithPrealloc(nil, 1<<20)
	require.NoError(t, err)
	require.Empty(t, out)
}
