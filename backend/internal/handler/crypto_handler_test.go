package handler

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
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

func TestCryptoConfigDestinationOnlyWritesChannelConfig(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{
		cryptopay.SettingKeyCryptoConfig: `{}`,
	}}
	h := &CryptoHandler{store: store, security: &cryptoConfigTestSecurity{}, operationNotifier: operationNotificationFunc(func(_ context.Context, action, phase string) error {
		require.Equal(t, "destination_change", action)
		require.Equal(t, "verified", phase)
		return nil
	})}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/v1/admin/crypto/config", strings.NewReader(
		`{"hot_wallet_evm":"0x2222222222222222222222222222222222222222"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-Crypto-TOTP", "123456")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	h.AdminUpdateConfig(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, []string{cryptopay.SettingKeyCryptoConfig}, store.writes)
}

func TestCryptoConfigCannotChangeGasWalletIndex(t *testing.T) {
	store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
	h := &CryptoHandler{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/v1/admin/crypto/config", strings.NewReader(`{"hot_index":1}`))
	c.Request.Header.Set("Content-Type", "application/json")
	h.AdminUpdateConfig(c)
	require.Equal(t, 400, w.Code)
	require.Empty(t, store.writes)
}

func TestCryptoAdminRejectsTrailingAndUnknownJSONBeforeActions(t *testing.T) {
	for _, endpoint := range []struct {
		name string
		call func(*CryptoHandler, *gin.Context)
		body string
	}{
		{"configuration", (*CryptoHandler).AdminUpdateConfig, `{"enabled":true}`},
		{"address status", (*CryptoHandler).AdminSetAddressStatus, `{"status":"disabled"}`},
		{"RPC probe", (*CryptoHandler).AdminTestRPC, `{"chain":"bsc","endpoint":"https://example.com"}`},
		{"RPC auto-pick", (*CryptoHandler).AdminAutoPickRPC, `{"chain":"bsc","want":1}`},
		{"2FA setup", (*CryptoHandler).AdminCryptoSecuritySetup, `{"password":"private-password"}`},
		{"2FA enrollment", (*CryptoHandler).AdminCryptoSecurityEnable, `{"totp_code":"123456","setup_token":"private-token"}`},
	} {
		for _, malformed := range []struct{ name, body string }{
			{"trailing object", endpoint.body + `{}`},
			{"unknown field", `{"unexpected":"private-value"}`},
			{"oversized", `{"unexpected":"` + strings.Repeat("x", 257<<10) + `"}`},
		} {
			t.Run(endpoint.name+"/"+malformed.name, func(t *testing.T) {
				store := &cryptoConfigStore{values: map[string]string{cryptopay.SettingKeyCryptoConfig: `{}`}}
				h := &CryptoHandler{store: store}
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", "/", strings.NewReader(malformed.body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Params = gin.Params{{Key: "id", Value: "1"}}
				endpoint.call(h, c)
				require.Equal(t, 400, w.Code, w.Body.String())
				require.Empty(t, store.writes)
				require.NotContains(t, w.Body.String(), "private-")
			})
		}
	}
}

func TestCryptoDepositCSVExportsAllPagesAndNeutralizesFormulas(t *testing.T) {
	var output bytes.Buffer
	var offsets []int
	err := writeCryptoDepositCSV(context.Background(), &output, func(_ context.Context, beforeID int64, limit int) ([]cryptopay.DepositRecord, error) {
		offsets = append(offsets, int(beforeID))
		require.Equal(t, 200, limit)
		if beforeID == 2 {
			return []cryptopay.DepositRecord{{ID: 1, FromAddress: "=malicious()", Amount: "0.25", Chain: "bsc"}}, nil
		}
		records := make([]cryptopay.DepositRecord, 200)
		for i := range records {
			records[i] = cryptopay.DepositRecord{ID: int64(201 - i), FromAddress: "=malicious()", Amount: "0.25", Chain: "bsc"}
		}
		return records, nil
	})
	require.NoError(t, err)
	require.Equal(t, []int{0, 2}, offsets)
	require.True(t, bytes.HasPrefix(output.Bytes(), []byte{0xEF, 0xBB, 0xBF}))
	rows, err := csv.NewReader(bytes.NewReader(output.Bytes()[3:])).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 202)
	require.Equal(t, "1", rows[201][0])
	require.Equal(t, "'=malicious()", rows[201][4])
}

func TestCryptoDepositCSVPropagatesDatabaseFailureAndCancellation(t *testing.T) {
	failure := errors.New("database offline")
	var output bytes.Buffer
	pages := 0
	err := writeCryptoDepositCSV(context.Background(), &output, func(_ context.Context, beforeID int64, _ int) ([]cryptopay.DepositRecord, error) {
		pages++
		if beforeID > 0 {
			return nil, failure
		}
		records := make([]cryptopay.DepositRecord, 200)
		for i := range records {
			records[i].ID = int64(200 - i)
		}
		return records, nil
	})
	require.ErrorIs(t, err, failure)
	require.Equal(t, 2, pages)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = writeCryptoDepositCSV(ctx, &output, func(context.Context, int64, int) ([]cryptopay.DepositRecord, error) {
		t.Fatal("a cancelled request must not query the database")
		return nil, nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCryptoDepositCSVRejectsInvalidOrRepeatingCursor(t *testing.T) {
	for _, ids := range [][]int64{{0}, {2, 2}, {1, 2}} {
		var output bytes.Buffer
		err := writeCryptoDepositCSV(context.Background(), &output, func(context.Context, int64, int) ([]cryptopay.DepositRecord, error) {
			rows := make([]cryptopay.DepositRecord, len(ids))
			for i, id := range ids {
				rows[i].ID = id
			}
			return rows, nil
		})
		require.ErrorContains(t, err, "cursor did not advance")
	}
}
