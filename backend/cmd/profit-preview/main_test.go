package main

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestParsePreviewInputsIgnoresNullUserOverride(t *testing.T) {
	raw := []byte(`{
		"groups": [{
			"group": {
				"id": 50,
				"name": "preview",
				"platform": "openai",
				"rate_multiplier": 0.5,
				"subscription_type": "standard",
				"profit_control_enabled": false,
				"profit_min_margin": 0.1,
				"profit_safety_buffer": 0
			},
			"accounts": [{
				"id": 1,
				"name": "cheap",
				"platform": "openai",
				"type": "apikey",
				"rate_multiplier": 0.2
			}],
			"user_overrides": {"40": null, "41": 0.4},
			"models": ["gpt-test"]
		}]
	}`)

	inputs, err := parsePreviewInputs(raw, true)
	require.NoError(t, err)
	require.Len(t, inputs, 1)
	require.Equal(t, map[int64]float64{41: 0.4}, inputs[0].UserOverrides)
	require.True(t, inputs[0].AssumeEnabled)

	report := service.PreviewProfitAdmission(inputs, time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC))[0]
	require.InDelta(t, 0.4, report.MinEffectiveD, 1e-12, "null 覆盖不能被解码成 0 倍率")
	require.InDelta(t, 0.36, report.ThresholdMinD, 1e-12)
}

func TestParsePreviewInputsRejectsEmptyGroups(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"groups":null}`),
		[]byte(`{"groups":[]}`),
	} {
		inputs, err := parsePreviewInputs(raw, false)
		require.ErrorContains(t, err, "input contains no groups")
		require.Nil(t, inputs)
	}
}
