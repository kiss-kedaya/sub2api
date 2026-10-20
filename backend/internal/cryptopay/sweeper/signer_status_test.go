package sweeper

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSignerStatusDistinguishesUnlockedFromSigningApproval(t *testing.T) {
	for _, body := range []string{
		`{"ok":true,"unlocked":true,"signing_enabled":false}`,
		`{"ok":true,"unlocked":true}`,
		`{"ok":true,"unlocked":true,"signing_enabled":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			client := &SignerClient{http: &http.Client{Transport: localSignerTransport{target: target}}}
			status, err := client.Status(t.Context())
			if err != nil || !status.Unlocked || status.SigningEnabled != (body == `{"ok":true,"unlocked":true,"signing_enabled":true}`) {
				t.Fatalf("status=%+v err=%v", status, err)
			}
		})
	}
}
