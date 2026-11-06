//go:build unit

package handler

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
)

func TestGrokVoiceTTSUsesUpstreamTextField(t *testing.T) {
	body := []byte(`{"input":"short","text":"the text upstream speaks"}`)
	if got, want := extractGrokTTSInputText(body), "the text upstream speaks"; got != want {
		t.Fatalf("moderation text = %q, want %q", got, want)
	}
	estimate := grokVoiceInflightEstimate("tts", body)
	if estimate.Kind != service.InflightEstimateAudio || estimate.AudioMode != "tts" {
		t.Fatalf("unexpected estimate shape: %#v", estimate)
	}
	if want := float64(len("the text upstream speaks")) / 1e6; estimate.AudioUnits != want {
		t.Fatalf("in-flight units = %v, want %v", estimate.AudioUnits, want)
	}
}

func TestIsExpectedGrokRealtimeClose(t *testing.T) {
	for _, status := range []coderws.StatusCode{
		coderws.StatusNormalClosure,
		coderws.StatusGoingAway,
		coderws.StatusNoStatusRcvd,
		coderws.StatusAbnormalClosure,
	} {
		if !isExpectedGrokRealtimeClose(coderws.CloseError{Code: status}) {
			t.Fatalf("status %v should be treated as an expected session close", status)
		}
	}
	if isExpectedGrokRealtimeClose(coderws.CloseError{Code: coderws.StatusPolicyViolation}) {
		t.Fatal("policy violations must not be treated as billable normal closes")
	}
}

func TestGrokRealtimeBillingResultRequiresObservedAudio(t *testing.T) {
	if grokRealtimeBillingResult("grok-voice-latest", time.Second, false) != nil {
		t.Fatal("a session without observed audio must not be billed")
	}
	if grokRealtimeBillingResult("grok-voice-latest", 0, true) != nil {
		t.Fatal("zero-duration sessions must not be billed")
	}
}

func TestGrokRealtimeBillingResultUsesForcedUniqueID(t *testing.T) {
	first := grokRealtimeBillingResult("grok-voice-latest", 90*time.Second, true)
	second := grokRealtimeBillingResult("grok-voice-latest", 90*time.Second, true)
	if first == nil || second == nil {
		t.Fatal("observed audio sessions should be billable")
	}
	if first.RequestID == "" {
		t.Fatalf("unexpected billing request ID %q", first.RequestID)
	}
	if first.RequestID == second.RequestID {
		t.Fatal("independent realtime connections must not share a billing request ID")
	}
	if first.AudioUsage == nil || first.AudioUsage.Mode != "realtime" || first.AudioUsage.DurationOrUnits != 1.5 {
		t.Fatalf("unexpected audio usage: %#v", first.AudioUsage)
	}
}
