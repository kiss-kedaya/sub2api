package service

import "context"

const LocalQualityModelPausesKey = "local_reasoning_model_pauses"
const OAuthAdmissionPauseKey = "oauth_admission_pause"

func HasLocalReasoningModelPause(a *Account, groupID *int64, model string) bool {
	if a == nil || model == "" {
		return false
	}
	owners, _ := a.Extra[LocalQualityModelPausesKey].(map[string]any)
	for _, raw := range owners {
		v, ok := raw.(map[string]any)
		if !ok || v["model"] != model {
			continue
		}
		g := int64(parseExtraFloat64(v["group_id"]))
		if g == 0 || groupID != nil && *groupID == g {
			return true
		}
	}
	return false
}
func HasOAuthAdmissionPause(a *Account) bool {
	if a == nil {
		return false
	}
	v, _ := a.Extra[OAuthAdmissionPauseKey].(map[string]any)
	token, _ := v["token"].(string)
	return token != ""
}

type localQualityGroupContextKey struct{}

func WithLocalQualityGroup(ctx context.Context, group *int64) context.Context {
	if group == nil {
		return context.WithValue(ctx, localQualityGroupContextKey{}, int64(0))
	}
	return context.WithValue(ctx, localQualityGroupContextKey{}, *group)
}
func LocalQualityGroupFromContext(ctx context.Context) *int64 {
	if ctx == nil {
		return nil
	}
	g, ok := ctx.Value(localQualityGroupContextKey{}).(int64)
	if !ok || g <= 0 {
		return nil
	}
	return &g
}
