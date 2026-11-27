//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestTimingNoRecorderSkipsOutcomeAndPreservesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		mw   gin.HandlerFunc
	}{
		{"no_recorder", RequestTiming()},
		{"nil_recorder", RequestTiming(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			var tw *timingWriter
			r.Use(tc.mw)
			r.POST("/v1/responses", func(c *gin.Context) {
				var ok bool
				tw, ok = c.Writer.(*timingWriter)
				require.True(t, ok)
				require.True(t, tw.skipOutcome)
				c.Header("Content-Type", "text/event-stream")
				_, err := c.Writer.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
				require.NoError(t, err)
				_, err = c.Writer.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
				require.NoError(t, err)
				c.Writer.Flush()
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			require.Contains(t, rec.Body.String(), `"response.completed"`)
			require.Nil(t, tw.outcome)
		})
	}
}

func TestRequestTimingWithRecorderCapturesOutcomeViaWriteString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := &diagnosticRecorder{}
	r := gin.New()
	r.Use(RequestTiming(rec))
	r.POST("/v1/responses", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		for _, line := range []string{
			"event: response.output_text.delta\n",
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n",
			"\n",
		} {
			_, err := c.Writer.WriteString(line)
			require.NoError(t, err)
		}
		c.Writer.Flush()
		for _, line := range []string{
			"event: response.completed\n",
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n",
			"\n",
		} {
			_, err := c.Writer.WriteString(line)
			require.NoError(t, err)
		}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, rec.rows, 1)
	require.Equal(t, "completed", rec.rows[0].Detail.Terminal)
	_, hasFlush := rec.rows[0].Detail.Events["first_output_flush"]
	require.True(t, hasFlush)
}
