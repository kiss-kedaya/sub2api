package httpclient

import (
	"net/http"
	"strings"
)

// EvictProxyClients removes clients for a temporary authenticated listener.
// Closing idle connections keeps active response bodies undisturbed.
func EvictProxyClients(proxyURL string) {
	prefix := strings.TrimSpace(proxyURL) + "|"
	if prefix == "|" {
		return
	}
	sharedClients.Range(func(key, value any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			if sharedClients.CompareAndDelete(key, value) {
				if client, ok := value.(*http.Client); ok {
					client.CloseIdleConnections()
				}
			}
		}
		return true
	})
}
