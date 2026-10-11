package bark

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSendPostsDocumentedFields(t *testing.T) {
	var gotPath string
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.WriteHeader(http.StatusOK)
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
