package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cryptoConfigStore struct {
	values map[string]string
	writes []string
}

func (s *cryptoConfigStore) GetCryptoSetting(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *cryptoConfigStore) SetCryptoSetting(_ context.Context, key, value string) error {
	s.writes = append(s.writes, key)
	s.values[key] = value
	return nil
}

func TestCryptoConfigCannotApproveSignerPayoutTarget(t *testing.T) {
	const policy = `{"targets":["0x1111111111111111111111111111111111111111"],"hot_index":0}`
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{}`,
		"crypto.signer.policy":           policy,
	}}
	h := &CryptoHandler{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/v1/admin/crypto/config", strings.NewReader(
		`{"hot_wallet_evm":"0x2222222222222222222222222222222222222222"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.AdminUpdateConfig(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, []string{cryptopay.SettingKeyCryptoConfig}, store.writes)
	require.Equal(t, policy, store.values["crypto.signer.policy"])
}
