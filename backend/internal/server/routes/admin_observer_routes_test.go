package routes

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise the real route registry and authentication, rather than a guessed
// collection of sensitive URL names. No handler or database mutation may run.
func TestObserverDeniedAcrossRegisteredManagementRoutes(t *testing.T) {
	router, tokens, _ := ticketRouteEnvironment(t)
	allowed := map[string]bool{
		"/api/v1/admin/usage": true, "/api/v1/admin/usage/stats": true,
		"/api/v1/admin/dashboard/stats": true, "/api/v1/admin/dashboard/realtime": true,
		"/api/v1/admin/dashboard/trend": true, "/api/v1/admin/dashboard/models": true,
		"/api/v1/admin/dashboard/groups": true, "/api/v1/admin/dashboard/snapshot-v2": true,
	}
	checked := 0
	for _, route := range router.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1/admin/") || route.Method == "GET" && allowed[route.Path] {
			continue
		}
		parts := strings.Split(route.Path, "/")
		for i, part := range parts {
			if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
				parts[i] = "1"
			}
		}
		w := ticketRouteRequest(router, route.Method, strings.Join(parts, "/"), tokens[9], `{}`)
		require.Equal(t, 403, w.Code, route.Method+" "+route.Path+" "+w.Body.String())
		require.Contains(t, w.Body.String(), "ADMIN_OBSERVER_READ_ONLY")
		checked++
	}
	require.Greater(t, checked, 150, "real administrator routes were not exercised")
}
