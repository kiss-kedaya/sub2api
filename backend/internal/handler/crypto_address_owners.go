package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// addressOwnerResolver is the part of cryptopay.Service the owners endpoint needs.
type addressOwnerResolver interface {
	LookupAddressOwners(ctx context.Context, addresses []string) (map[string]cryptopay.AddressOwner, error)
}

// AdminAddressOwners resolves deposit addresses to the users they were
// assigned to, so the console can show who a deposit or sweep belongs to.
// GET /api/v1/admin/crypto/address-owners?address=0x..&address=0x..
//
// The response carries user emails. They appear here and nowhere else: not in
// logs, not in error text.
func (h *CryptoHandler) AdminAddressOwners(c *gin.Context) {
	if h.service == nil {
		response.BadRequest(c, "crypto channel unavailable")
		return
	}
	serveAddressOwners(c, h.service)
}

func serveAddressOwners(c *gin.Context, resolver addressOwnerResolver) {
	owners, err := resolver.LookupAddressOwners(c.Request.Context(), c.QueryArray("address"))
	switch {
	case errors.Is(err, cryptopay.ErrTooManyOwnerAddresses):
		response.BadRequest(c, "too many addresses")
		return
	case err != nil:
		// Fixed text on both sides. The cause is deliberately not printed: the
		// rows being read hold user identities.
		slog.WarnContext(c.Request.Context(), "[cryptopay] address owner lookup failed")
		response.Error(c, http.StatusInternalServerError, "address owner lookup failed")
		return
	}
	if owners == nil {
		owners = map[string]cryptopay.AddressOwner{}
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"owners": owners}})
}
