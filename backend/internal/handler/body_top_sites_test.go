package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil/jsonbody"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Differential test for the migrated handler call sites: each function runs once
// with a nil gin context (BodyTop forwards to gjson: the code path before the
// top-level index) and once with a fresh context (the index answers), on random
// request-shaped bodies padded past the real size threshold. Results must be equal.

var topSitePad = strings.Repeat("x", 17<<10)

// padTopBody inserts a large member right after the opening brace so the body is
// indexed at the production threshold; bodies without an object are left as is
// (they exercise the "not an object" fallback).
func padTopBody(body []byte) []byte {
	s := string(body)
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return body
	}
	rest := strings.TrimLeft(s[i+1:], " \t\r\n")
	sep := ","
	if strings.HasPrefix(rest, "}") {
		sep = ""
	}
	return []byte(s[:i+1] + `"_pad":"` + topSitePad + `"` + sep + s[i+1:])
}

// newTopHandlerTestContext is a context like a plain HTTP request's: RequestBodyTop
// leaves contexts without a request (and WebSocket ones) alone.
func newTopHandlerTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestMigratedHandlerSitesMatchGJSON(t *testing.T) {
	const bodies = 25000
	sites := []struct {
		name string
		run  func(c *gin.Context, body []byte) string
	}{
		{"requestMaxOutputTokens", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(requestMaxOutputTokens(c, body))
		}},
		{"tokenInflightEstimate", func(c *gin.Context, body []byte) string {
			return fmt.Sprintf("%+v", tokenInflightEstimate(c, "m", body))
		}},
		{"parseOpenAICompatibleStream", func(c *gin.Context, body []byte) string {
			stream, ok := parseOpenAICompatibleStream(c, body)
			return fmt.Sprint(stream, ok)
		}},
		{"codexBootstrapInputLooksRelevant", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(codexBootstrapInputLooksRelevant(c, body))
		}},
		{"isOpenAIRemoteCompactionV2Request", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(isOpenAIRemoteCompactionV2Request(c, body))
		}},
		{"entry-point lookups (validity, model, previous_response_id)", func(c *gin.Context, body []byte) string {
			top := service.RequestBodyTop(c, body)
			model := top.Get("model")
			return fmt.Sprint(top.Valid(), model.Exists(), model.Type, model.String(), top.Get("previous_response_id").String())
		}},
	}
	for si, site := range sites {
		site := site
		t.Run(site.name, func(t *testing.T) {
			g := jsonbody.New(int64(9000 + si))
			indexed := 0
			for i := 0; i < bodies; i++ {
				body := padTopBody(g.Body())
				want := site.run(nil, body)
				c := newTopHandlerTestContext()
				got := site.run(c, body)
				if _, ok := c.Get("__sub2api_body_top_memo"); ok {
					indexed++
				}
				if got != want {
					t.Fatalf("%s, body #%d\n  gjson path: %s\n  index path: %s\n  body (first 400 bytes): %.400q", site.name, i, want, got, body)
				}
			}
			// The comparison says nothing if the index never ran.
			require.Greaterf(t, indexed*100, bodies*50, "%s asked the index for only %d of %d bodies", site.name, indexed, bodies)
		})
	}
}
