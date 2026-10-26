package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type livePresentationTestStore struct {
	liveTestStore
}

func (s *livePresentationTestStore) UpdateLiveSessionRequestedModel(_ context.Context, callHash, owner, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil || s.record.CallHash != callHash || s.record.ControllerOwner != owner || s.record.Controller != LiveControllerProxy {
		return ErrLiveControllerChanged
	}
	s.record.SessionRequestedModel = model
	return nil
}

func TestCreateLiveCallPersistsOriginalModelAndActualGroupPresentationPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			group := &Group{ID: 44, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
				SubscriptionType: SubscriptionTypeStandard, AllowLive: true, ReturnRequestedModel: enabled}
			key := liveTestAPIKey(group)
			account := Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Schedulable: true, Concurrency: 2, GroupIDs: []int64{group.ID},
				Credentials: map[string]any{"access_token": "test-access-token", "chatgpt_account_id": "acct_test"}}
			cache := &liveCreateTestCache{closeOnSave: true}
			upstream := &liveHTTPUpstreamStub{}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.Scheduling.LoadBatchEnabled = false
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: liveCreateTestAccountRepo{account: account},
				cache: cache, concurrencyService: NewConcurrencyService(&liveCreateTestConcurrencyCache{}), httpUpstream: upstream,
				liveAttestation: liveAttestationStub{header: "attestation"}, liveAttestationCipher: liveTestCipher{}}
			created, err := svc.CreateLiveCall(t.Context(), &LiveCallRequest{SDP: "v=0\r\n",
				Session: json.RawMessage(`{"model":"upstream-live-v3"}`), RequestedModel: "Codex-Live-Alias[1M]"},
				LiveCallIdentity{APIKey: key, APIKeyID: key.ID, UserID: key.UserID, GroupID: key.GroupID, RouteGroupIDs: key.RouteGroupIDs}, 2)
			require.NoError(t, err)
			require.NotNil(t, created)
			stored, err := cache.GetLiveCall(t.Context(), hashLiveCallID(created.CallID))
			require.NoError(t, err)
			require.Equal(t, "upstream-live-v3", stored.Model, "billing/observation keeps the upstream name")
			require.Equal(t, "Codex-Live-Alias[1M]", stored.RequestedModel)
			require.Equal(t, stored.RequestedModel, stored.SessionRequestedModel)
			require.Equal(t, enabled, stored.ReturnRequestedModel)
			require.Equal(t, "upstream-live-v3", gjson.GetBytes(upstream.body, "session.model").Str)
		})
	}
}

func TestLiveModelPresentationPreservesConcurrentResponsesAndSessionAcknowledgements(t *testing.T) {
	p := newLiveModelPresentation(&LiveCallRecord{RequestedModel: "Claude-Alias[1M]", ReturnRequestedModel: true})
	require.NoError(t, p.observeClient([]byte(`{"event_id":"create-a","type":"response.create","response":{"model":"DeepSeek-V3-Alias"}}`)))
	model, changed := p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_a","model":"provider-a"}}`))
	require.Equal(t, "DeepSeek-V3-Alias", model)
	require.False(t, changed)
	require.NoError(t, p.observeClient([]byte(`{"type":"session.update","session":{"model":"Gemini-Version-2026"}}`)))
	model, changed = p.responseModel([]byte(`{"type":"session.updated","session":{"model":"provider-b"}}`))
	require.Equal(t, "Gemini-Version-2026", model)
	require.True(t, changed)
	require.NoError(t, p.observeClient([]byte(`{"type":"response.create"}`)))
	model, _ = p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_b","model":"provider-b"}}`))
	require.Equal(t, "Gemini-Version-2026", model)
	model, _ = p.responseModel([]byte(`{"type":"response.done","response":{"id":"resp_a","model":"provider-a"}}`))
	require.Equal(t, "DeepSeek-V3-Alias", model, "a session update cannot rename an already active response")
	model, _ = p.responseModel([]byte(`{"type":"response.done","response":{"id":"resp_b","model":"provider-b"}}`))
	require.Equal(t, "Gemini-Version-2026", model)
}

func TestLiveModelPresentationRejectedSessionUpdateDoesNotChangeFallback(t *testing.T) {
	p := newLiveModelPresentation(&LiveCallRecord{RequestedModel: "Initial-Alias", ReturnRequestedModel: true})
	require.NoError(t, p.observeClient([]byte(`{"event_id":"bad-update","type":"session.update","session":{"model":"Rejected-Alias"}}`)))
	model, changed := p.responseModel([]byte(`{"type":"error","error":{"event_id":"bad-update","message":"rejected"}}`))
	require.Empty(t, model)
	require.False(t, changed)
	require.NoError(t, p.observeClient([]byte(`{"type":"response.create"}`)))
	model, _ = p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_a","model":"actual"}}`))
	require.Equal(t, "Initial-Alias", model)
}

func TestLiveModelPresentationRejectedUpdateDoesNotLeakIntoInheritedQueuedModels(t *testing.T) {
	p := newLiveModelPresentation(&LiveCallRecord{RequestedModel: "Initial-Alias", ReturnRequestedModel: true})
	require.NoError(t, p.observeClient([]byte(`{"event_id":"bad-update","type":"session.update","session":{"model":"Rejected-Alias"}}`)))
	require.NoError(t, p.observeClient([]byte(`{"event_id":"instructions-only","type":"session.update","session":{"instructions":"leave text"}}`)))
	// This response inherited the pending session before its rejection arrived.
	require.NoError(t, p.observeClient([]byte(`{"event_id":"inherited-response","type":"response.create"}`)))
	model, changed := p.responseModel([]byte(`{"type":"error","error":{"event_id":"bad-update","message":"rejected"}}`))
	require.Empty(t, model)
	require.False(t, changed)
	model, changed = p.responseModel([]byte(`{"type":"session.updated","session":{"model":"actual-initial"}}`))
	require.Equal(t, "Initial-Alias", model, "instructions-only updates cannot inherit a rejected model")
	require.True(t, changed)
	// A later accepted model must not rename the already queued response either.
	require.NoError(t, p.observeClient([]byte(`{"event_id":"new-update","type":"session.update","session":{"model":"New-Alias"}}`)))
	model, changed = p.responseModel([]byte(`{"type":"session.updated","session":{"model":"actual-new"}}`))
	require.Equal(t, "New-Alias", model)
	require.True(t, changed)
	model, _ = p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_inherited","model":"actual-initial"}}`))
	require.Equal(t, "Initial-Alias", model)
	model, _ = p.responseModel([]byte(`{"type":"response.done","response":{"id":"resp_inherited","model":"actual-initial"}}`))
	require.Equal(t, "Initial-Alias", model)
	require.NoError(t, p.observeClient([]byte(`{"type":"response.create"}`)))
	model, _ = p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_new","model":"actual-new"}}`))
	require.Equal(t, "New-Alias", model)
}

func TestLiveModelPresentationUncorrelatedErrorDiscardsRejectedPendingModels(t *testing.T) {
	for _, rejected := range []string{
		`{"type":"response.create","response":{"model":"Rejected-Alias"}}`,
		`{"type":"session.update","session":{"model":"Rejected-Alias"}}`,
	} {
		t.Run(gjson.Get(rejected, "type").Str, func(t *testing.T) {
			p := newLiveModelPresentation(&LiveCallRecord{RequestedModel: "Initial-Alias", ReturnRequestedModel: true})
			require.NoError(t, p.observeClient([]byte(`{"type":"response.create","response":{"model":"Active-Alias"}}`)))
			model, _ := p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_active","model":"actual-active"}}`))
			require.Equal(t, "Active-Alias", model)
			require.NoError(t, p.observeClient([]byte(rejected)))
			errorPayload := []byte(`{"type":"error","model":"actual-error","error":{"message":"rejected"}}`)
			model, changed := p.responseModel(errorPayload)
			require.Empty(t, model)
			require.False(t, changed)
			require.Equal(t, string(errorPayload), string(RewriteRequestedModelResponse(errorPayload, "realtime", model)))
			require.NoError(t, p.observeClient([]byte(`{"type":"response.create","response":{"model":"Next-Alias"}}`)))
			model, _ = p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_next","model":"actual-next"}}`))
			require.Equal(t, "Next-Alias", model, "the rejected request may not consume the next response's position")
			model, _ = p.responseModel([]byte(`{"type":"response.done","response":{"id":"resp_active","model":"actual-active"}}`))
			require.Equal(t, "Active-Alias", model, "an unrelated error cannot rename an active response")
			require.NoError(t, p.observeClient([]byte(`{"type":"response.create"}`)))
			model, _ = p.responseModel([]byte(`{"type":"response.created","response":{"id":"resp_default","model":"actual-initial"}}`))
			require.Equal(t, "Initial-Alias", model, "uncorrelated errors revert speculative session models")
		})
	}
}

func TestLiveModelPresentationConcurrentDirectionsAndPendingBounds(t *testing.T) {
	p := newLiveModelPresentation(&LiveCallRecord{RequestedModel: "Original-Alias"})
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for range 64 {
			if err := p.observeClient([]byte(`{"type":"session.update","session":{"model":"New-Alias"}}`)); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for range 64 {
			p.responseModel([]byte(`{"type":"session.updated","session":{"model":"provider"}}`))
		}
	}()
	workers.Wait()
	bounded := newLiveModelPresentation(&LiveCallRecord{RequestedModel: "Original-Alias"})
	for range 128 {
		require.NoError(t, bounded.observeClient([]byte(`{"type":"response.create"}`)))
	}
	require.Error(t, bounded.observeClient([]byte(`{"type":"response.create"}`)))
}

func TestLiveSidebandRequestedModelRelayPreservesPayloadsAndPersistsSessionModel(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "test-access-token", "chatgpt_account_id": "acct_test"}}
			record := &LiveCallRecord{CallID: "call_model", CallHash: hashLiveCallID("call_model"), AccountID: account.ID,
				APIKeyID: 22, UserID: 33, LeaseID: "lease", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
				Controller: LiveControllerPending, Model: "upstream-v3", RequestedModel: "Codex-Alias[1M]",
				SessionRequestedModel: "Codex-Alias[1M]", ReturnRequestedModel: enabled, AttestationCiphertext: "enc:attestation"}
			store := &livePresentationTestStore{}
			require.NoError(t, store.SaveLiveCall(t.Context(), record, time.Hour))
			upstream := newLiveTestFrameConn()
			svc := &OpenAIGatewayService{accountRepo: &liveTestAccountRepo{account: account}, cache: store,
				openaiWSPassthroughDialer: &liveTestDialer{conn: upstream}, liveAttestationCipher: liveTestCipher{}}
			result := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				downstream, err := coderws.Accept(w, request, nil)
				if err != nil {
					result <- err
					return
				}
				defer func() { _ = downstream.CloseNow() }()
				result <- svc.ProxyLiveSideband(request.Context(), record, downstream)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer func() { _ = client.CloseNow() }()
			exchange := func(body, path, expected string) {
				t.Helper()
				upstream.reads <- liveTestFrame{messageType: coderws.MessageText, payload: []byte(body)}
				_, actual, readErr := client.Read(ctx)
				require.NoError(t, readErr)
				if enabled && path != "" {
					require.Equal(t, expected, gjson.GetBytes(actual, path).Str)
					if usage := gjson.Get(body, "response.usage"); usage.Exists() {
						require.Equal(t, usage.Raw, gjson.GetBytes(actual, "response.usage").Raw)
					}
					require.Equal(t, gjson.Get(body, "session.instructions").Raw, gjson.GetBytes(actual, "session.instructions").Raw)
				} else {
					require.Equal(t, body, string(actual))
				}
			}
			exchange(`{"type":"session.created","session":{"model":"upstream-v3","instructions":"upstream-v3"}}`, "session.model", "Codex-Alias[1M]")
			update := []byte(`{"type":"session.update","session":{"model":"Gemini-Cased-v2","instructions":"leave text"}}`)
			require.NoError(t, client.Write(ctx, coderws.MessageText, update))
			select {
			case actual := <-upstream.writes:
				require.Equal(t, string(update), string(actual.payload))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			exchange(`{"type":"session.updated","session":{"model":"provider-session","instructions":"provider-session"}}`, "session.model", "Gemini-Cased-v2")
			create := []byte(`{"type":"response.create","response":{"model":"DeepSeek-Alias-v3"}}`)
			require.NoError(t, client.Write(ctx, coderws.MessageText, create))
			select {
			case actual := <-upstream.writes:
				require.Equal(t, string(create), string(actual.payload))
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			exchange(`{"type":"response.created","response":{"id":"resp_model","model":"provider-response","usage":{"total_tokens":9}}}`, "response.model", "DeepSeek-Alias-v3")
			exchange(`{"type":"response.audio.delta","delta":"YXVkaW8="}`, "", "")
			exchange(`{"type":"error","model":"provider-error","error":{"message":"provider-error"}}`, "", "")
			upstream.reads <- liveTestFrame{messageType: coderws.MessageBinary, payload: []byte{1, 2, 3}}
			messageType, binary, readErr := client.Read(ctx)
			require.NoError(t, readErr)
			require.Equal(t, coderws.MessageBinary, messageType)
			require.Equal(t, []byte{1, 2, 3}, binary)
			stored, err := store.GetLiveCall(ctx, record.CallHash)
			require.NoError(t, err)
			wantSession := record.SessionRequestedModel
			if enabled {
				wantSession = "Gemini-Cased-v2"
			}
			require.Equal(t, wantSession, stored.SessionRequestedModel)
			require.Equal(t, "upstream-v3", stored.Model)
			if enabled {
				rejoined := newLiveModelPresentation(stored)
				model, _ := rejoined.responseModel([]byte(`{"type":"session.created","session":{"model":"provider-session"}}`))
				require.Equal(t, "Gemini-Cased-v2", model)
			}
			_ = client.CloseNow()
			select {
			case <-result:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
