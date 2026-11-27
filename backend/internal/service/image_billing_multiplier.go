package service

import "math"

func resolveImageRateMultiplier(apiKey *APIKey, effectiveGroupMultiplier float64) float64 {
	if apiKey != nil && apiKey.Group != nil && apiKey.Group.ImageRateIndependent {
		if apiKey.Group.ImageRateMultiplier < 0 {
			return 0
		}
		return apiKey.Group.ImageRateMultiplier
	}
	return effectiveGroupMultiplier
}

func resolveVideoRateMultiplier(apiKey *APIKey, effectiveGroupMultiplier float64) float64 {
	if apiKey != nil && apiKey.Group != nil && apiKey.Group.VideoRateIndependent {
		if apiKey.Group.VideoRateMultiplier < 0 {
			return 0
		}
		return apiKey.Group.VideoRateMultiplier
	}
	return effectiveGroupMultiplier
}

func resolveToolRateMultiplier(apiKey *APIKey, effectiveGroupMultiplier float64) float64 {
	var group *Group
	if apiKey != nil {
		group = apiKey.Group
	}
	return ResolveGroupToolRateMultiplier(group, effectiveGroupMultiplier)
}

// ResolveGroupToolRateMultiplier returns the effective customer multiplier for
// tool endpoints (DeepSeek search/fetch/ocr/tts/asr). Only DeepSeek groups honor
// ToolRateIndependent/ToolRateMultiplier; other platforms always fall back to
// effectiveGroupMultiplier. Non-finite or negative values are sanitized.
func ResolveGroupToolRateMultiplier(group *Group, effectiveGroupMultiplier float64) float64 {
	if group != nil && group.Platform == PlatformDeepseek && group.ToolRateIndependent {
		if math.IsNaN(group.ToolRateMultiplier) || math.IsInf(group.ToolRateMultiplier, 0) {
			return 1
		}
		if group.ToolRateMultiplier < 0 {
			return 0
		}
		return group.ToolRateMultiplier
	}
	if math.IsNaN(effectiveGroupMultiplier) || math.IsInf(effectiveGroupMultiplier, 0) {
		return 1
	}
	if effectiveGroupMultiplier < 0 {
		return 0
	}
	return effectiveGroupMultiplier
}
