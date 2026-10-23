package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Existing single-request fixtures explicitly implement the same atomic store
// contract. Production never falls back to their unconditional Set method.
func (s *cryptoConfigStore) GetCryptoSettingUncached(ctx context.Context, key string) (string, error) {
	return s.GetCryptoSetting(ctx, key)
}

func (s *cryptoConfigStore) CompareAndSwapCryptoSetting(_ context.Context, key string, expected *string, value string) (bool, error) {
	current, exists := s.values[key]
	if (expected == nil && exists) || (expected != nil && (!exists || current != *expected)) {
		return false, nil
	}
	s.writes = append(s.writes, key)
	s.values[key] = value
	return true, nil
}

type cryptoCASDatabase struct {
	mu           sync.Mutex
	values       map[string]string
	reads        int
	initialReads chan struct{}
	releaseReads chan struct{}
	events       []string
}

type cryptoCASRepository struct {
	service.SettingRepository
	db        *cryptoCASDatabase
	label     string
	beforeCAS func(context.Context) error
	afterCAS  func()
	readErr   error
	writeErr  error
	callbacks atomic.Int32
}

func (r *cryptoCASRepository) GetValue(context.Context, string) (string, error) {
	return "", errors.New("configuration edits must bypass a replica's cached settings")
}

func (r *cryptoCASRepository) Set(context.Context, string, string) error {
	return errors.New("configuration edits must never use unconditional Set")
}

func (r *cryptoCASRepository) GetValueUncached(ctx context.Context, key string) (string, error) {
	if r.readErr != nil {
		return "", r.readErr
	}
	r.db.mu.Lock()
	value, exists := r.db.values[key]
	r.db.reads++
	wait := r.db.initialReads != nil && r.db.reads <= 2
	r.db.mu.Unlock()
	if wait {
		r.db.initialReads <- struct{}{}
		select {
		case <-r.db.releaseReads:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if !exists {
		return "", service.ErrSettingNotFound
	}
	return value, nil
}

func (r *cryptoCASRepository) CompareAndSwapValue(ctx context.Context, key string, expected *string, value string) (bool, error) {
	if r.beforeCAS != nil {
		if err := r.beforeCAS(ctx); err != nil {
			return false, err
		}
	}
	if r.writeErr != nil {
		return false, r.writeErr
	}
	r.db.mu.Lock()
	current, exists := r.db.values[key]
	if (expected == nil && exists) || (expected != nil && (!exists || current != *expected)) {
		r.db.mu.Unlock()
		return false, nil
	}
	r.db.values[key] = value
	r.db.events = append(r.db.events, "saved:"+r.label)
	r.db.mu.Unlock()
	if r.afterCAS != nil {
		r.afterCAS()
	}
	return true, nil
}

type cryptoCASNotification struct {
	db  *cryptoCASDatabase
	err error
}

func (n *cryptoCASNotification) NotifyWalletOperation(_ context.Context, action, phase string) error {
	n.db.mu.Lock()
	n.db.events = append(n.db.events, "notified:"+action+":"+phase)
	n.db.mu.Unlock()
	return n.err
}

func newCryptoCASHandler(repo *cryptoCASRepository, notifier cryptoWalletOperationNotifier) *CryptoHandler {
	settings := service.NewSettingService(repo, nil)
	settings.SetOnUpdateCallback(func() { repo.callbacks.Add(1) })
	return &CryptoHandler{store: cryptopay.NewServiceSettingStore(settings), operationNotifier: notifier, security: &cryptoConfigTestSecurity{}}
}

func TestCryptoConfigConcurrentReplicasRejectStalePatchAndPreserveDestinationNotice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, winner := range []string{"destination", "rpc"} {
		t.Run(winner+" commits first", func(t *testing.T) {
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			const oldAddress = "0x1111111111111111111111111111111111111111"
			const newAddress = "0x2222222222222222222222222222222222222222"
			db := &cryptoCASDatabase{
				values:       map[string]string{cryptopay.SettingKeyCryptoConfig: `{"hot_wallet_evm":"` + oldAddress + `","rpc":{"bsc":"https://old.example.test"}}`},
				initialReads: make(chan struct{}, 2), releaseReads: make(chan struct{}),
			}
			notifier := &cryptoCASNotification{db: db}
			firstCommitted := make(chan struct{})
			var committedOnce sync.Once
			repos := map[string]*cryptoCASRepository{}
			servers := map[string]*httptest.Server{}
			for _, label := range []string{"destination", "rpc"} {
				repo := &cryptoCASRepository{db: db, label: label}
				if label == winner {
					repo.afterCAS = func() { committedOnce.Do(func() { close(firstCommitted) }) }
				} else {
					repo.beforeCAS = func(ctx context.Context) error {
						select {
						case <-firstCommitted:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					}
				}
				repos[label] = repo
				router := gin.New()
				router.Use(cryptoConfigTestIdentity)
				router.PUT("/config", newCryptoCASHandler(repo, notifier).AdminUpdateConfig)
				server := httptest.NewServer(router)
				defer server.Close()
				servers[label] = server
			}
			patches := map[string]string{
				"destination": `{"hot_wallet_evm":"` + newAddress + `"}`,
				"rpc":         `{"rpc":{"bsc":"https://new.example.test"}}`,
			}
			type result struct {
				label, body string
				status      int
				err         error
			}
			results := make(chan result, 2)
			send := func(label string) result {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodPut, servers[label].URL+"/config", strings.NewReader(patches[label]))
				if err != nil {
					return result{label: label, err: err}
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Crypto-TOTP", "123456")
				resp, err := servers[label].Client().Do(req)
				if err != nil {
					return result{label: label, err: err}
				}
				defer func() { _ = resp.Body.Close() }()
				body, err := io.ReadAll(resp.Body)
				return result{label: label, body: string(body), status: resp.StatusCode, err: err}
			}
			for _, label := range []string{"destination", "rpc"} {
				go func() { results <- send(label) }()
			}
			for range 2 {
				select {
				case <-db.initialReads:
				case <-time.After(3 * time.Second):
					t.Fatal("both replicas must load the same original configuration")
				}
			}
			close(db.releaseReads)
			loser := "rpc"
			if winner == "rpc" {
				loser = "destination"
			}
			for range 2 {
				got := <-results
				require.NoError(t, got.err)
				if got.label == winner {
					require.Equal(t, http.StatusOK, got.status, got.body)
				} else {
					require.Equal(t, http.StatusConflict, got.status, got.body)
					require.Equal(t, "CRYPTO_CONFIG_CONFLICT", gjson.Get(got.body, "reason").String())
					require.Contains(t, got.body, "刷新")
					require.NotContains(t, got.body, oldAddress)
					require.NotContains(t, got.body, "old.example.test")
				}
			}
			require.Equal(t, int32(1), repos[winner].callbacks.Load())
			require.Zero(t, repos[loser].callbacks.Load())
			db.mu.Lock()
			firstValue := db.values[cryptopay.SettingKeyCryptoConfig]
			db.mu.Unlock()
			if winner == "destination" {
				require.Equal(t, newAddress, gjson.Get(firstValue, "hot_wallet_evm").String())
				require.Equal(t, "https://old.example.test", gjson.Get(firstValue, "rpc.bsc").String())
			} else {
				require.Equal(t, oldAddress, gjson.Get(firstValue, "hot_wallet_evm").String())
				require.Equal(t, "https://new.example.test", gjson.Get(firstValue, "rpc.bsc").String())
			}
			// A new explicit request rereads current state and merges only its
			// patch. It preserves the other replica's destination/RPC change.
			retry := send(loser)
			require.NoError(t, retry.err)
			require.Equal(t, http.StatusOK, retry.status, retry.body)
			db.mu.Lock()
			finalValue := db.values[cryptopay.SettingKeyCryptoConfig]
			events := append([]string(nil), db.events...)
			db.mu.Unlock()
			require.Equal(t, newAddress, gjson.Get(finalValue, "hot_wallet_evm").String())
			require.Equal(t, "https://new.example.test", gjson.Get(finalValue, "rpc.bsc").String())
			saved := 0
			notified := 0
			for _, event := range events {
				if event == "notified:destination_change:verified" {
					notified++
				}
				if event == "saved:destination" {
					require.Positive(t, notified, "destination commits require a prior verified notification: %v", events)
				}
				if strings.HasPrefix(event, "saved:") {
					saved++
				}
			}
			require.Equal(t, 2, saved, "the rejected patch must not write")
			require.Equal(t, int32(1), repos[loser].callbacks.Load())
		})
	}
}

func TestCryptoConfigCASDoesNotWriteOnNotificationReadOrWriteFailure(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, failure := range []string{"notification", "read", "write"} {
		t.Run(failure, func(t *testing.T) {
			const original = `{"hot_wallet_evm":"0x1111111111111111111111111111111111111111"}`
			db := &cryptoCASDatabase{values: map[string]string{cryptopay.SettingKeyCryptoConfig: original}}
			repo := &cryptoCASRepository{db: db, label: "destination"}
			notifier := &cryptoCASNotification{db: db}
			switch failure {
			case "notification":
				notifier.err = errors.New("notification unavailable")
			case "read":
				repo.readErr = errors.New("read unavailable")
			case "write":
				repo.writeErr = errors.New("write unavailable")
			}
			router := gin.New()
			router.Use(cryptoConfigTestIdentity)
			router.PUT("/config", newCryptoCASHandler(repo, notifier).AdminUpdateConfig)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"hot_wallet_evm":"0x2222222222222222222222222222222222222222"}`))
			request.Header.Set("X-Crypto-TOTP", "123456")
			router.ServeHTTP(response, request)
			require.GreaterOrEqual(t, response.Code, 500, response.Body.String())
			require.Equal(t, original, db.values[cryptopay.SettingKeyCryptoConfig])
			require.Zero(t, repo.callbacks.Load())
			for _, event := range db.events {
				require.False(t, strings.HasPrefix(event, "saved:"))
			}
		})
	}
}

func TestCryptoConfigCASKeepsOmittedFieldsAndSupportsFirstSave(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, initial := range []map[string]string{{}, {cryptopay.SettingKeyCryptoConfig: ""}} {
		db := &cryptoCASDatabase{values: initial}
		repo := &cryptoCASRepository{db: db, label: "rpc"}
		router := gin.New()
		router.Use(cryptoConfigTestIdentity)
		router.PUT("/config", newCryptoCASHandler(repo, nil).AdminUpdateConfig)
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"rpc":{"bsc":"https://first.example.test"}}`))
		request.Header.Set("X-Crypto-TOTP", "123456")
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var cfg cryptopay.CryptoConfig
		require.NoError(t, json.Unmarshal([]byte(db.values[cryptopay.SettingKeyCryptoConfig]), &cfg))
		require.Equal(t, "https://first.example.test", cfg.RPC[cryptopay.ChainBSC])
		require.False(t, cfg.Enabled)
		require.Equal(t, cryptopay.DefaultCryptoConfig().CreditMultiplier, cfg.CreditMultiplier)
	}
}
