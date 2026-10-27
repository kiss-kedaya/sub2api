package ip

import (
	"github.com/gin-gonic/gin"
	"net/netip"
)

const verifiedGatewayIPKey = "sub2api.verified_gateway_client_ip"

// SetVerifiedGatewayIP is only called after internal gateway HMAC and nonce
// verification. No public forwarding header sets this request-local identity.
func SetVerifiedGatewayIP(c *gin.Context, raw string) bool {
	address, err := netip.ParseAddr(raw)
	if c == nil || err != nil || address.Zone() != "" {
		return false
	}
	c.Set(verifiedGatewayIPKey, address.Unmap().String())
	return true
}
func verifiedGatewayIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString(verifiedGatewayIPKey)
}
