package handler

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSecurityAuditBindsConstraintAcrossCachedHTTPAudit(t *testing.T) {
	c, key, selected := routeBindingFixture()
	decision := bindSecurityAuditGroupConstraint(c, key, securityaudit.Decision{Kind: securityaudit.DecisionAllow, AllowNextStage: true, RestrictedGroupID: selected.GroupID})
	require.True(t, decision.AllowNextStage)
	c.Set(securityAuditCompletedContextKey, true)
	require.Nil(t, runSecurityAudit(c, nil, nil, nil, key, middleware.AuthSubject{UserID: key.UserID}, "openai", "model", nil, "http"))
	// Even the old same-group early return must enforce the target before
	// billing, preauthorization or forwarding. Nil providers must not be touched.
	err := bindSelectedKeyRoute(c, nil, nil, nil, keyRouteBinding{Previous: key, Selected: key})
	require.ErrorIs(t, err, service.ErrSecurityPenaltyGroupRestricted)
	require.NoError(t, service.CheckSecurityPenaltySelectedGroup(c.Request.Context(), selected))
}

func TestSecurityAuditRejectsUnboundPenaltyTarget(t *testing.T) {
	c, key, selected := routeBindingFixture()
	key.RouteGroupIDs = []int64{*key.GroupID}
	decision := bindSecurityAuditGroupConstraint(c, key, securityaudit.Decision{Kind: securityaudit.DecisionAllow, AllowNextStage: true, RestrictedGroupID: selected.GroupID})
	require.False(t, decision.AllowNextStage)
	require.Equal(t, http.StatusForbidden, decision.HTTPStatus)
	require.Equal(t, "security_penalty_group_restricted", decision.ErrorCode)
}

func TestPenaltyTargetBindingKeepsSelectedSubscriptionAndBillingChecks(t *testing.T) {
	c, key, selected := routeBindingFixture()
	decision := bindSecurityAuditGroupConstraint(c, key, securityaudit.Decision{Kind: securityaudit.DecisionAllow, AllowNextStage: true, RestrictedGroupID: selected.GroupID})
	require.True(t, decision.AllowNextStage)
	checker := &routeBindingEligibilityStub{err: service.ErrInsufficientBalance}
	gateway := &routeBindingGatewayStub{}
	err := bindSelectedKeyRoute(c, gateway, checker, nil, keyRouteBinding{Previous: key, Selected: selected})
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.Equal(t, []int64{*selected.GroupID}, checker.groups)
	bound, _ := middleware.GetAPIKeyFromContext(c)
	require.Same(t, key, bound, "failed admission must not rebind the billing identity")
}

func TestPenaltyGroupForbiddenIsNotAnUpstreamOrBillingError(t *testing.T) {
	fallback := noAccountErrorClassification{Status: http.StatusServiceUnavailable, ErrType: "api_error", Message: "unavailable"}
	got := classifySelectionFailureErrorFromGin(nil, service.ErrSecurityPenaltyGroupRestricted, fallback)
	require.Equal(t, http.StatusForbidden, got.Status)
	require.Equal(t, "security_penalty_group_restricted", got.ErrType)
	got = classifySelectionFailureErrorFromGin(nil, errors.New("upstream HTTP 403"), fallback)
	require.Equal(t, fallback, got, "arbitrary upstream errors must preserve their original path")
	status, code, _, _ := billingErrorDetails(service.ErrSecurityPenaltyGroupRestricted)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "security_penalty_group_restricted", code)
}
