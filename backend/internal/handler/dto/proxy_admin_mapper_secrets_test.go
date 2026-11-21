package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Proxy list/detail responses must never carry the decrypted password: the
// credential is encrypted at rest and only its presence is reported.
func TestProxyFromServiceAdminMasksPassword(t *testing.T) {
	proxy := &service.Proxy{ID: 7, Name: "edge", Protocol: "http", Host: "10.0.0.1", Port: 8080, Username: "user", Password: "live-password"}

	admin := ProxyFromServiceAdmin(proxy)
	require.NotNil(t, admin)
	require.Empty(t, admin.Password, "plaintext proxy password must not be projected to the admin API")
	require.True(t, admin.HasPassword)
	require.Equal(t, "live-password", proxy.Password, "the service value stays plaintext")

	empty := ProxyFromServiceAdmin(&service.Proxy{ID: 8, Name: "no-auth", Protocol: "http", Host: "10.0.0.2", Port: 8081})
	require.NotNil(t, empty)
	require.False(t, empty.HasPassword)
	require.Empty(t, empty.Password)
}
