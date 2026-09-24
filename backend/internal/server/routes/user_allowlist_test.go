package routes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserAllowlistRoutesRemoved(t *testing.T) {
	router, tokens, _ := ticketRouteEnvironment(t)
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/user/cf-allowlist"},
		{http.MethodPost, "/api/v1/user/cf-allowlist"},
		{http.MethodDelete, "/api/v1/user/cf-allowlist/1"},
	} {
		t.Run(route.method, func(t *testing.T) {
			response := ticketRouteRequest(router, route.method, route.path, tokens[7], "")
			require.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}
