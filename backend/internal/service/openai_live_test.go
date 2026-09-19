package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type liveHTTPUpstreamStub struct {
	request *http.Request
	body    []byte
}

type liveAttestationStub struct {
	header string
	err    error
}

type liveTestCipher struct{}

func (liveTestCipher) Encrypt(plaintext string) (string, error) { return "enc:" + plaintext, nil }
func (liveTestCipher) Decrypt(ciphertext string) (string, error) {
	return strings.TrimPrefix(ciphertext, "enc:"), nil
}

type liveCreateTestCache struct {
	schedulerTestGatewayCache
	store       liveTestStore
	closeOnSave bool
}

func (c *liveCreateTestCache) SaveLiveCall(ctx context.Context, record *LiveCallRecord, ttl time.Duration) error {
	if err := c.store.SaveLiveCall(ctx, record, ttl); err != nil {
		return err
	}
	if c.closeOnSave {
		c.store.mu.Lock()
		c.store.record.Controller = LiveControllerClosed
		c.store.mu.Unlock()
	}
	return nil
}

func (c *liveCreateTestCache) GetLiveCall(ctx context.Context, callHash string) (*LiveCallRecord, error) {
	return c.store.GetLiveCall(ctx, callHash)
}

func (c *liveCreateTestCache) ClaimLiveController(ctx context.Context, callHash, controller, owner string) (bool, error) {
	return c.store.ClaimLiveController(ctx, callHash, controller, owner)
}

func (c *liveCreateTestCache) ReleaseLiveController(ctx context.Context, callHash, owner string) (bool, error) {
	return c.store.ReleaseLiveController(ctx, callHash, owner)
}

func (c *liveCreateTestCache) GetLiveController(ctx context.Context, callHash string) (string, error) {
	return c.store.GetLiveController(ctx, callHash)
}

func (c *liveCreateTestCache) MarkLiveCallClosed(ctx context.Context, callHash string, ttl time.Duration) (bool, error) {
	return c.store.MarkLiveCallClosed(ctx, callHash, ttl)
}

type liveCreateTestConcurrencyCache struct {
	schedulerTestConcurrencyCache
	live liveTestConcurrencyCache
}

type liveCreateTestAccountRepo struct {
	AccountRepository
	account Account
}

func (r liveCreateTestAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account.ID != id {
		return nil, errors.New("account not found")
	}
	account := r.account
	return &account, nil
}

func (r liveCreateTestAccountRepo) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	for _, id := range r.account.GroupIDs {
		if id == groupID {
			return []Account{r.account}, nil
		}
	}
	return nil, nil
}

func (r liveCreateTestAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	if r.account.Platform != platform {
		return nil, nil
	}
	return r.ListSchedulableByGroupID(context.Background(), groupID)
}

func (r liveCreateTestAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]Account, error) {
	if r.account.Platform != platform {
		return nil, nil
	}
	return []Account{r.account}, nil
}

func (r liveCreateTestAccountRepo) ListSchedulableUngroupedByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}

func (c *liveCreateTestConcurrencyCache) AcquireLiveLease(
	ctx context.Context,
	accountID int64,
	accountMax int,
	userID int64,
	userMax int,
	apiKeyID int64,
	leaseID string,
	replacingRegularSlots bool,
) (bool, error) {
	return c.live.AcquireLiveLease(ctx, accountID, accountMax, userID, userMax, apiKeyID, leaseID, replacingRegularSlots)
}

func (c *liveCreateTestConcurrencyCache) RefreshLiveLease(ctx context.Context, accountID, userID, apiKeyID int64, leaseID string) (bool, error) {
	return c.live.RefreshLiveLease(ctx, accountID, userID, apiKeyID, leaseID)
}

func (c *liveCreateTestConcurrencyCache) ReleaseLiveLease(ctx context.Context, accountID, userID, apiKeyID int64, leaseID string) error {
	return c.live.ReleaseLiveLease(ctx, accountID, userID, apiKeyID, leaseID)
}

func liveTestAPIKey(group *Group) *APIKey {
	user := &User{ID: 33, Status: StatusActive}
	groupID := group.ID
	return &APIKey{
		ID: 22, UserID: user.ID, Status: StatusActive, Key: "secret-live-key",
		User: user, GroupID: &groupID, Group: group, RouteGroupIDs: []int64{groupID},
	}
}

func (s liveAttestationStub) Check(context.Context) error {
	return s.err
}

func (s liveAttestationStub) Generate(context.Context) (string, error) {
	return s.header, s.err
}

func (s *liveHTTPUpstreamStub) Do(
	request *http.Request,
	_ string,
	_ int64,
	_ int,
) (*http.Response, error) {
	s.request = request
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	s.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Location": {"/backend-api/codex/call_test"},
		},
		Body: io.NopCloser(strings.NewReader("v=0\r\n")),
	}, nil
}

func (s *liveHTTPUpstreamStub) DoWithTLS(
	request *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return s.Do(request, proxyURL, accountID, accountConcurrency)
}

func TestLiveCapabilityOnlyAllowsOpenAIOAuth(t *testing.T) {
	require.True(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{Platform: PlatformGrok, Type: AccountTypeOAuth}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			openAIAuthModeCredentialKey: OpenAIAuthModePersonalAccessToken,
		},
	}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
	require.False(t, (&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity,
		},
	}).SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityLive))
}

func TestValidateLiveCallRequestDoesNotRequireDelegation(t *testing.T) {
	request := &LiveCallRequest{
		SDP:     "v=0\r\n",
		Session: json.RawMessage(`{"model":"gpt-live-test","instructions":"hello"}`),
	}
	require.NoError(t, ValidateLiveCallRequest(request))
	require.NotContains(t, string(request.Session), "delegation")
}

func TestCreateLiveCallStandardModeRejectsUnpricedBillingBeforeUpstream(t *testing.T) {
	tests := []struct {
		name             string
		subscriptionType string
		subscriptionID   *int64
	}{
		{name: "balance", subscriptionType: SubscriptionTypeStandard},
		{name: "subscription", subscriptionType: SubscriptionTypeSubscription, subscriptionID: func() *int64 { value := int64(9); return &value }()},
	}
	request := &LiveCallRequest{SDP: "v=0\r\n", Session: json.RawMessage(`{"model":"gpt-live-test"}`)}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			group := &Group{
				ID: 44, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
				SubscriptionType: test.subscriptionType, AllowLive: true,
			}
			key := liveTestAPIKey(group)
			upstream := &liveHTTPUpstreamStub{}
			svc := &OpenAIGatewayService{cfg: &config.Config{RunMode: config.RunModeStandard}, httpUpstream: upstream}
			_, err := svc.CreateLiveCall(context.Background(), request, LiveCallIdentity{
				APIKey: key, APIKeyID: key.ID, UserID: key.UserID, GroupID: key.GroupID,
				RouteGroupIDs: key.RouteGroupIDs, SubscriptionID: test.subscriptionID,
			}, 2)
			require.ErrorIs(t, err, ErrLiveBillingNotConfigured)
			require.Nil(t, upstream.request, "unpriced standard Live must not dial upstream")
		})
	}
}

func TestCreateLiveCallSimpleModeUsesCompleteAPIKeyForCandidateSelection(t *testing.T) {
	group := &Group{
		ID: 44, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
		SubscriptionType: SubscriptionTypeStandard, AllowLive: true,
	}
	key := liveTestAPIKey(group)
	account := Account{
		ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{group.ID},
		Credentials: map[string]any{"access_token": "test-access-token", "chatgpt_account_id": "acct_test"},
	}
	cache := &liveCreateTestCache{closeOnSave: true}
	concurrencyCache := &liveCreateTestConcurrencyCache{}
	upstream := &liveHTTPUpstreamStub{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		cfg: cfg, accountRepo: liveCreateTestAccountRepo{account: account},
		cache: cache, concurrencyService: NewConcurrencyService(concurrencyCache), httpUpstream: upstream,
		liveAttestation: liveAttestationStub{header: "attestation"}, liveAttestationCipher: liveTestCipher{},
	}

	created, err := svc.CreateLiveCall(context.Background(), &LiveCallRequest{
		SDP: "v=0\r\n", Session: json.RawMessage(`{"model":"gpt-live-test"}`),
	}, LiveCallIdentity{
		APIKey: key, APIKeyID: key.ID, UserID: key.UserID,
		GroupID: key.GroupID, RouteGroupIDs: key.RouteGroupIDs,
	}, 2)

	require.NoError(t, err)
	require.NotNil(t, created)
	require.Equal(t, account.ID, created.Account.ID)
	require.NotNil(t, upstream.request)
	require.NotNil(t, cache.store.record)
}

func TestLiveCallIdentityDoesNotSerializeAPIKeyCredentials(t *testing.T) {
	group := &Group{ID: 44, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowLive: true}
	key := liveTestAPIKey(group)
	raw, err := json.Marshal(LiveCallIdentity{APIKey: key, APIKeyID: key.ID, UserID: key.UserID})
	require.NoError(t, err)
	require.NotContains(t, string(raw), key.Key)
	var encoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &encoded))
	require.NotContains(t, encoded, "APIKey")
}

func TestValidateLiveCallIdentityRequiresExactOwner(t *testing.T) {
	group := &Group{ID: 44, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowLive: true}
	key := liveTestAPIKey(group)
	valid := LiveCallIdentity{
		APIKey: key, APIKeyID: key.ID, UserID: key.UserID,
		GroupID: key.GroupID, RouteGroupIDs: key.RouteGroupIDs,
	}
	require.NoError(t, validateLiveCallIdentity(valid))

	tests := []struct {
		name   string
		mutate func(*LiveCallIdentity)
	}{
		{name: "api key", mutate: func(identity *LiveCallIdentity) { identity.APIKeyID++ }},
		{name: "user", mutate: func(identity *LiveCallIdentity) { identity.UserID++ }},
		{name: "group", mutate: func(identity *LiveCallIdentity) { other := int64(45); identity.GroupID = &other }},
		{name: "routes", mutate: func(identity *LiveCallIdentity) { identity.RouteGroupIDs = []int64{45} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := valid
			test.mutate(&identity)
			require.ErrorIs(t, validateLiveCallIdentity(identity), ErrLiveIdentityMismatch)
		})
	}
}

func TestLiveRouteGroupRequiresAllowLive(t *testing.T) {
	group := &Group{ID: 44, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, AllowLive: true}
	key := liveTestAPIKey(group)
	require.True(t, liveRouteGroupAllowed(key))
	group.AllowLive = false
	require.False(t, liveRouteGroupAllowed(key))
	group.AllowLive = true
	group.Platform = PlatformAnthropic
	require.False(t, liveRouteGroupAllowed(key))
}

func TestCreateUpstreamLiveCallPreservesSession(t *testing.T) {
	upstream := &liveHTTPUpstreamStub{}
	service := &OpenAIGatewayService{
		cfg:          &config.Config{},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          7,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 2,
		Credentials: map[string]any{
			"access_token":       "test-access-token",
			"chatgpt_account_id": "acct_test",
		},
	}
	session := json.RawMessage(`{
		"model":"gpt-live-test",
		"delegation":{"type":"client"},
		"custom":{"keep":true}
	}`)

	created, err := service.createUpstreamLiveCall(context.Background(), account, &LiveCallRequest{
		SDP:     "v=offer\r\n",
		Session: session,
	}, `{"v":1,"s":0,"t":"v1.test"}`)
	require.NoError(t, err)
	require.Equal(t, "call_test", created.CallID)
	require.Equal(t, []byte("v=0\r\n"), created.SDP)

	var forwarded struct {
		SDP     string          `json:"sdp"`
		Session json.RawMessage `json:"session"`
	}
	require.NoError(t, json.Unmarshal(upstream.body, &forwarded))
	require.Equal(t, "v=offer\r\n", forwarded.SDP)
	require.JSONEq(t, string(session), string(forwarded.Session))
	require.Equal(t, "Bearer test-access-token", upstream.request.Header.Get("Authorization"))
	require.Equal(t, "acct_test", upstream.request.Header.Get("Chatgpt-Account-Id"))
	require.Equal(t, "quicksilver=v2", upstream.request.Header.Get("OpenAI-Alpha"))
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, upstream.request.Header.Get(liveAttestationHeader))
	require.NotEmpty(t, upstream.request.Header.Get("Session-Id"))
	require.NotEmpty(t, upstream.request.Header.Get("Thread-Id"))
	require.Empty(t, upstream.request.Header.Get("OpenAI-Beta"))
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.request.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
}

func TestLiveAttestationCipherRoundTripAndRejectsOtherInstanceKey(t *testing.T) {
	first := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "first-live-secret"},
	})
	second := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "second-live-secret"},
	})
	require.NotNil(t, first)
	require.NotNil(t, second)

	ciphertext, err := first.Encrypt(`{"v":1,"s":0,"t":"v1.opaque"}`)
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "opaque")

	plaintext, err := first.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.opaque"}`, plaintext)

	_, err = second.Decrypt(ciphertext)
	require.Error(t, err)
}

func TestPrepareLiveAttestationEncryptsHeaderAndReturnsExplicitProviderError(t *testing.T) {
	cipher := newLiveAttestationCipher(&config.Config{
		JWT: config.JWTConfig{Secret: "live-attestation-test-secret"},
	})
	service := &OpenAIGatewayService{
		liveAttestation:       liveAttestationStub{header: `{"v":1,"s":0,"t":"v1.test"}`},
		liveAttestationCipher: cipher,
	}
	header, ciphertext, err := service.prepareLiveAttestation(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{"v":1,"s":0,"t":"v1.test"}`, header)
	require.NotContains(t, ciphertext, "v1.test")
	decrypted, err := cipher.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, header, decrypted)

	service.liveAttestation = liveAttestationStub{err: errors.New("macOS app missing")}
	_, _, err = service.prepareLiveAttestation(context.Background())
	var unavailable *LiveAttestationUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Contains(t, unavailable.Error(), "macOS app missing")
}

func TestLiveMaxSessionDurationDefaultsAndOverrides(t *testing.T) {
	require.Equal(t, defaultLiveMaxSessionDuration, (&OpenAIGatewayService{}).liveMaxSessionDuration())
	require.Equal(
		t,
		90*time.Second,
		(&OpenAIGatewayService{cfg: &config.Config{
			Gateway: config.GatewayConfig{
				Live: config.GatewayLiveConfig{MaxSessionDurationSeconds: 90},
			},
		}}).liveMaxSessionDuration(),
	)
}

func TestLiveSidebandNormalCloseEndsCall(t *testing.T) {
	normalClose := coderws.CloseError{Code: coderws.StatusNormalClosure}
	require.ErrorIs(t, liveSidebandReadError(normalClose), ErrLiveCallNotFound)

	abnormalClose := coderws.CloseError{Code: coderws.StatusInternalError}
	require.Equal(t, abnormalClose, liveSidebandReadError(abnormalClose))
}

func TestLiveCreateFailoverUsesExistingOpenAIPolicy(t *testing.T) {
	service := &OpenAIGatewayService{}
	account := newOpenAIUpstreamErrorTestAccount()
	require.False(t, service.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{
		StatusCode:   http.StatusBadRequest,
		ResponseBody: []byte(`{"error":{"message":"invalid session"}}`),
	}))
	require.True(t, service.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{
		StatusCode: http.StatusForbidden,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(account, &UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}))
	require.True(t, service.shouldFailoverLiveCreateError(account, errors.New("transport failed")))
}

func TestLiveCallIDFromLocation(t *testing.T) {
	callID, err := liveCallIDFromLocation("https://chatgpt.com/backend-api/codex/call_123?intent=quicksilver")
	require.NoError(t, err)
	require.Equal(t, "call_123", callID)

	callID, err = liveCallIDFromLocation("/backend-api/codex/call_456")
	require.NoError(t, err)
	require.Equal(t, "call_456", callID)
}

func TestRequestTypeLive(t *testing.T) {
	require.True(t, RequestTypeLive.IsValid())
	require.Equal(t, "live", RequestTypeLive.String())
	parsed, err := ParseUsageRequestType("live")
	require.NoError(t, err)
	require.Equal(t, RequestTypeLive, parsed)
}
