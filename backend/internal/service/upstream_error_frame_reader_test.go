package service

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Generate arbitrarily many short data lines without allocating a giant input.
// There is deliberately no event boundary or EOF: the reader's error-event
// bound, rather than input exhaustion, must stop consumption.
type repeatingErrorDataReader struct {
	line   string
	offset int
	read   int
}

func (r *repeatingErrorDataReader) Read(target []byte) (int, error) {
	for n := 0; n < len(target); {
		copied := copy(target[n:], r.line[r.offset:])
		n += copied
		r.offset = (r.offset + copied) % len(r.line)
	}
	r.read += len(target)
	return len(target), nil
}

func TestUpstreamErrorFrameReaderOptionalErrorLimitBoundsConsumption(t *testing.T) {
	const limit = 64 << 10
	for _, header := range []string{"event: error\n", "id: test\nevent:error\n"} {
		t.Run(strings.TrimSpace(header), func(t *testing.T) {
			flood := &repeatingErrorDataReader{line: "data: \"incomplete multiline diagnostic\",\n"}
			source := io.MultiReader(strings.NewReader(header), flood)
			reader := newUpstreamErrorFrameReaderWithErrorLimit(source, 32<<20, limit)
			body, err := io.ReadAll(reader)
			require.ErrorIs(t, err, errUpstreamErrorFrameTooLarge)
			require.Empty(t, body, "oversized error content must not reach the downstream")
			require.LessOrEqual(t, flood.read, limit+8192, "the optional bound must apply before allocating the ordinary frame limit")
		})
	}
}

func TestUpstreamErrorFrameReaderErrorLimitDoesNotLimitNormalContent(t *testing.T) {
	const limit = 64 << 10
	content := `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"event: error ` + strings.Repeat("x", limit*2) + `"}}` + "\n\n"
	body, err := io.ReadAll(newUpstreamErrorFrameReaderWithErrorLimit(strings.NewReader(content), 1<<20, limit))
	require.NoError(t, err)
	require.Equal(t, content, string(body), "a string inside model content is not an SSE event header")
	// Every existing caller keeps the original behavior unless it explicitly
	// requests the error-specific cap.
	errorEvent := "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"" + strings.Repeat("x", limit*2) + "\"}}\n\n"
	body, err = io.ReadAll(newUpstreamErrorFrameReader(strings.NewReader(errorEvent), 1<<20))
	require.NoError(t, err)
	require.Equal(t, errorEvent, string(body))
}
