package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func penaltyRouteFixture() (*APIKey, *Group, *Group) {
	primary := &Group{ID: 10, Status: StatusActive, Hydrated: true, Platform: PlatformOpenAI}
	target := &Group{ID: 20, Status: StatusActive, Hydrated: true, Platform: PlatformOpenAI}
	key := &APIKey{ID: 8, UserID: 7, User: &User{ID: 7}, GroupID: &primary.ID, Group: primary, RouteGroupIDs: []int64{10, 20}}
	return key, primary, target
}

func TestPenaltyGroupConstraintCannotGrantOrTransferAuthorization(t *testing.T) {
	key, primary, target := penaltyRouteFixture()
	ctx, err := ContextWithSecurityPenaltyGroupConstraint(context.Background(), key, &target.ID)
	require.NoError(t, err)
	require.ErrorIs(t, CheckSecurityPenaltySelectedGroup(ctx, key), ErrSecurityPenaltyGroupRestricted)
	routed := cloneAPIKeyWithGroupID(key, target)
	require.NoError(t, CheckSecurityPenaltySelectedGroup(ContextWithAPIKeyRoute(ctx, routed), routed))
	require.Equal(t, primary.ID, *key.GroupID, "the audit cannot rewrite the authenticated key")
	other := *routed
	other.ID++
	require.ErrorIs(t, CheckSecurityPenaltySelectedGroup(ctx, &other), ErrSecurityPenaltyGroupRestricted)
	other = *routed
	other.UserID++
	require.ErrorIs(t, CheckSecurityPenaltySelectedGroup(ctx, &other), ErrSecurityPenaltyGroupRestricted)
	target.IsExclusive = true
	require.ErrorIs(t, CheckSecurityPenaltySelectedGroup(ctx, routed), ErrSecurityPenaltyGroupRestricted, "revoked user ACL remains authoritative")
	key.RouteGroupIDs = []int64{primary.ID}
	_, err = ContextWithSecurityPenaltyGroupConstraint(context.Background(), key, &target.ID)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted, "an unbound target is never granted")
	require.NoError(t, CheckSecurityPenaltySelectedGroup(context.Background(), nil), "disabled feature changes no existing route")
	cleared, err := ContextWithSecurityPenaltyGroupConstraint(ctx, key, nil)
	require.NoError(t, err)
	require.NoError(t, CheckSecurityPenaltySchedulingGroup(cleared, &primary.ID), "a freshly audited WebSocket turn can clear an expired restriction")
}

func TestPenaltyRouteIteratorEnforcesTargetBeforeCatalogAcrossPlatforms(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformGrok, PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			key, primary, target := penaltyRouteFixture()
			primary.Platform, target.Platform = platform, platform
			ctx, err := ContextWithSecurityPenaltyGroupConstraint(context.Background(), key, &target.ID)
			require.NoError(t, err)
			var hydrated []int64
			routes := newAPIKeyRouteIterator(ctx, key, key.CandidateGroupIDs(), "model",
				func(_ context.Context, k *APIKey, id int64) (*APIKey, error) {
					hydrated = append(hydrated, id)
					require.Equal(t, target.ID, id)
					return cloneAPIKeyWithGroupID(k, target), nil
				}, nil,
				func(_ context.Context, id *int64) groupModelsCatalog {
					require.Equal(t, target.ID, *id)
					return groupModelsCatalog{byPlatform: map[string][]string{platform: {"model"}}, platforms: map[string]struct{}{platform: {}}}
				},
				func(context.Context, *int64, string) (ChannelMappingResult, bool) {
					return ChannelMappingResult{}, false
				}, nil)
			candidate, ok := routes.next()
			require.True(t, ok)
			require.Equal(t, target.ID, *candidate.key.GroupID)
			_, ok = routes.next()
			require.False(t, ok, "exhausting the target must never unlock another candidate")
			require.Equal(t, []int64{target.ID}, hydrated)
		})
	}
}

func TestPenaltyRouteIteratorRejectsFallbackAndRevokedTarget(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "revoked", true: "fallback"}[fallback], func(t *testing.T) {
			key, primary, target := penaltyRouteFixture()
			ctx, err := ContextWithSecurityPenaltyGroupConstraint(context.Background(), key, &target.ID)
			require.NoError(t, err)
			if !fallback {
				target.IsExclusive = true
			}
			routes := newAPIKeyRouteIterator(ctx, key, key.CandidateGroupIDs(), "model",
				func(_ context.Context, k *APIKey, _ int64) (*APIKey, error) {
					return cloneAPIKeyWithGroupID(k, target), nil
				},
				func(_ context.Context, k *APIKey) (*APIKey, error) {
					if fallback {
						return cloneAPIKeyWithGroupID(k, primary), nil
					}
					return k, nil
				},
				func(context.Context, *int64) groupModelsCatalog {
					t.Fatal("denied group reached the account catalog")
					return groupModelsCatalog{}
				}, nil, nil)
			_, ok := routes.next()
			require.False(t, ok)
			require.ErrorIs(t, routes.err, ErrSecurityPenaltyGroupRestricted)
		})
	}
}

func TestPenaltyConstraintPrecedesStickyAndPreviousResponseLookups(t *testing.T) {
	key, primary, target := penaltyRouteFixture()
	ctx, err := ContextWithSecurityPenaltyGroupConstraint(context.Background(), key, &target.ID)
	require.NoError(t, err)
	// Nil services intentionally panic if any database/cache/scheduler is touched.
	var gateway *GatewayService
	_, err = gateway.SelectAccountForModelWithExclusions(ctx, &primary.ID, "old-sticky", "model", nil)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted)
	_, err = gateway.SelectAccountWithLoadAwareness(ctx, &primary.ID, "old-sticky", "model", nil, "", 7)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted)
	var openai *OpenAIGatewayService
	_, err = openai.selectAccountForModelWithExclusions(ctx, &primary.ID, PlatformOpenAI, "old-sticky", "model", nil, false, 123, OpenAIEndpointCapabilityResponses, false)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted)
	_, err = openai.selectAccountWithLoadAwareness(ctx, &primary.ID, PlatformOpenAI, "old-sticky", "model", nil, false, OpenAIEndpointCapabilityResponses, false)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted)
	_, _, err = openai.selectAccountWithSchedulerOnce(ctx, &primary.ID, "resp_old", "old-sticky", "model", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, "", false, PlatformOpenAI, false, false)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted)
}

func TestPenaltyConstraintBlocksOfficialClaudeFallback(t *testing.T) {
	key, primary, target := penaltyRouteFixture()
	target.ClaudeCodeOnly, target.FallbackGroupID = true, &primary.ID
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{primary, target})
	gateway := &GatewayService{schedulerSnapshot: snapshot}
	ctx, err := ContextWithSecurityPenaltyGroupConstraint(SetClaudeCodeClient(context.Background(), false), key, &target.ID)
	require.NoError(t, err)
	_, _, err = gateway.resolveGatewayGroup(withSchedulerSnapshotOnly(ctx), &target.ID)
	require.ErrorIs(t, err, ErrSecurityPenaltyGroupRestricted)
}

func TestPenaltyOpenAIKeyRouteDoesNotEscapeUnavailableTarget(t *testing.T) {
	key, primary, target := penaltyRouteFixture()
	snapshot, _ := newSmartRouteCoreSnapshot([]*Group{primary, target})
	gateway := &OpenAIGatewayService{schedulerSnapshot: snapshot}
	ctx, err := ContextWithSecurityPenaltyGroupConstraint(context.Background(), key, &target.ID)
	require.NoError(t, err)
	var attempts []int64
	_, _, _, err = gateway.selectAlongKeyRoutes(ctx, key, nil, "model", func(_ context.Context, id *int64, _ []string, _ string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		attempts = append(attempts, *id)
		return nil, OpenAIAccountScheduleDecision{}, ErrNoAvailableAccounts
	})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{target.ID}, attempts)
}
