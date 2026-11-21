package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminRotateAddressTemporarilyBlocked(t *testing.T) {
	router := gin.New()
	router.POST("/addresses/:id/rotate", (&CryptoHandler{}).AdminRotateAddress)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/addresses/42/rotate", nil))
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "DEPOSIT_ADDRESS_ROTATION_UNAVAILABLE")
}
