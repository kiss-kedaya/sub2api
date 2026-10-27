package serverless

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestServerlessTargetsNeedExplicitPrivateTrustAndAlwaysBlockMetadata(t *testing.T) {
	m, err := New(Runtime{}, nil, &memoryStore{})
	require.NoError(t, err)
	defer m.Stop()
	for _, raw := range []string{"http://127.0.0.1:9000", "https://10.1.2.3", "https://169.254.169.254", "https://metadata.google.internal", "https://[::ffff:169.254.169.254]"} {
		require.Error(t, m.validateTarget(raw))
	}
	m.runtime.TrustedPrivateCIDRs = []string{"10.0.0.0/8", "127.0.0.0/8"}
	require.NoError(t, m.validateTarget("http://10.1.2.3:9000"))
	require.False(t, m.allowIP(netip.MustParseAddr("169.254.169.254")))
	require.False(t, m.allowIP(netip.MustParseAddr("100.100.100.200")))
	require.Error(t, ValidateRuntime(Runtime{TrustedPrivateCIDRs: []string{"0.0.0.0/0"}}))
	require.Error(t, ValidateRuntime(Runtime{TrustedPrivateCIDRs: []string{"10.0.0.0/1"}}))
}
func TestServerlessProofBindsBodyOriginHostAndAuthentication(t *testing.T) {
	m := managerFor(t, "us-pod", cacheFor(t), &memoryStore{})
	for _, field := range []string{"body", "host", "authorization", "origin"} {
		t.Run(field, func(t *testing.T) {
			req := testRequest("POST", "/v1/responses", strings.NewReader(`{"input":"before"}`))
			req.Header.Set("Authorization", "Bearer fixture")
			req.Header.Set("Origin", "https://client.example")
			ticket := ticket{Node: m.runtime.ID, Boot: m.boot, IP: "203.0.113.4", At: time.Now().Unix(), Nonce: randomID()}
			ticket.Body, _ = m.bodyDigest(req)
			ticket.Sig = m.proof(ticket, req)
			if field == "body" {
				req.Body = io.NopCloser(strings.NewReader(`{"input":"after"}`))
			}
			if field == "host" {
				req.Host = "other.example"
			}
			if field == "authorization" {
				req.Header.Set("Authorization", "Bearer different")
			}
			if field == "origin" {
				req.Header.Set("Origin", "https://other.example")
			}
			raw, _ := json.Marshal(ticket)
			req.Header.Set(forwardHeader, string(raw))
			router := gin.New()
			router.Use(m.Ingress())
			router.POST("/v1/responses", func(c *gin.Context) { c.Status(200) })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, 403, w.Code)
		})
	}
}
