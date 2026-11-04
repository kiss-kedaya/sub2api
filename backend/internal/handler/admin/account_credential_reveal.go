package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// revealableCredentialKey is the only credential the admin account editor may
// read back in place: a plain upstream API key. OAuth tokens, cookies, private
// keys and service-account JSON stay write-only on this surface; they are only
// exportable through the whole-installation export that already requires
// step-up 2FA.
const revealableCredentialKey = "api_key"

// ErrCredentialNotRevealable is returned for account types that do not hold a
// plain API key (OAuth / setup-token / service account / spark shadows).
var ErrCredentialNotRevealable = errors.New("this account does not store a revealable API key")

type revealCredentialResponse struct {
	CredentialKey string `json:"credential_key"`
	Value         string `json:"value"`
}

// revealAPIKey returns the stored api_key for a credential-owning account.
// It never falls back to another credential key, so a handler bug cannot leak
// a refresh token through the API-key field.
func revealAPIKey(account *service.Account) (string, error) {
	if account == nil || account.ParentAccountID != nil {
		return "", ErrCredentialNotRevealable
	}
	raw, ok := account.Credentials[revealableCredentialKey]
	if !ok {
		return "", ErrCredentialNotRevealable
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", ErrCredentialNotRevealable
	}
	return value, nil
}

// RevealAPIKey handles GET /api/v1/admin/accounts/:id/credential.
//
// The route is wrapped in the same step-up 2FA gate as the installation
// export, so the plaintext key is only served to a session that verified a
// fresh TOTP code (15 minute grant) and never to an admin API key.
func (h *AccountHandler) RevealAPIKey(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	value, err := revealAPIKey(account)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	// The response is a live secret: never let a browser, proxy or shared cache
	// keep it.
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private")
	c.Header("Pragma", "no-cache")
	response.Success(c, revealCredentialResponse{
		CredentialKey: revealableCredentialKey,
		Value:         value,
	})
}
