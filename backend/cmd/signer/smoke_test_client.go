//go:build ignore

// Smoke client for manual verification against a running signer.
// Usage: go run smoke_test_client.go <socket-path>
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: go run smoke_test_client.go <socket-path>")
		os.Exit(1)
	}
	socket := os.Args[1]
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}

	resp, err := client.Get("http://signer/healthz")
	if err != nil {
		fmt.Println("health error:", err)
		os.Exit(1)
	}
	fmt.Println("health:", resp.Status)
	resp.Body.Close()

	body := `{"start":0,"count":3}`
	resp, err = client.Post("http://signer/derive", "application/json", strings.NewReader(body))
	if err != nil {
		fmt.Println("derive error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	buf := make([]byte, 2048)
	n, _ := resp.Body.Read(buf)
	fmt.Println("derive:", resp.Status, string(buf[:n]))
}
