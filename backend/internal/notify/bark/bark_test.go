package bark

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fixtureFailingTransport struct{}

func (fixtureFailingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("synthetic offline transport failure")
}

func TestSendPostsDocumentedFields(t *testing.T) {
	var gotPath string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	defer server.Close()

	client := New("test-device-key")
	client.SetBaseURL(server.URL)
	err := client.Send(context.Background(), Notification{
		Title: "T", Subtitle: "S", Body: "B", Group: "g", Level: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/test-device-key" {
		t.Fatalf("path = %q", gotPath)
	}
	for key, want := range map[string]string{"title": "T", "subtitle": "S", "body": "B", "group": "g", "level": "active"} {
		if gotForm.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, gotForm.Get(key), want)
		}
	}
}

func TestNilClientAndEmptyKeyAreNoOps(t *testing.T) {
	if New("") != nil {
		t.Fatal("empty device key must yield nil client")
	}
	var nilClient *Client
	if err := nilClient.Send(context.Background(), Notification{Title: "x"}); err != nil {
		t.Fatalf("nil client send = %v, want nil", err)
	}
}

func TestSendRejectsNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := New("k")
	client.SetBaseURL(server.URL)
	if err := client.Send(context.Background(), Notification{Title: "x"}); err == nil {
		t.Fatal("expected error for http 500")
	}
}

func TestSendRequiresBarkSuccessCode(t *testing.T) {
	for _, body := range []string{"", `{"code":400,"message":"invalid key"}`, `{"code":500}`, `not-json`, `{}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := New("fixture")
			client.SetBaseURL(server.URL)
			if err := client.Send(context.Background(), Notification{Title: "security"}); err == nil {
				t.Fatal("a security alert must not acknowledge an unconfirmed delivery")
			}
		})
	}
}

func TestSendTransportFailureDoesNotLeakDeviceKey(t *testing.T) {
	const key = "bark-fixture-device-secret"
	client := New(key)
	client.http.Transport = fixtureFailingTransport{}
	err := client.Send(context.Background(), Notification{Title: "fixture"})
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("expected sanitized transport error, got %v", err)
	}
	if !strings.Contains(err.Error(), "transport failed") {
		t.Fatalf("expected useful generic transport error, got %v", err)
	}
}

func TestBodyIsSent(t *testing.T) {
	// Guards against a regression where the form is built but never written.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "hello") {
			t.Errorf("body missing payload: %q", body)
		}
	}))
	defer server.Close()
	client := New("k")
	client.SetBaseURL(server.URL)
	_ = client.Send(context.Background(), Notification{Body: "hello"})
}
