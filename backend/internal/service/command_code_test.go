package service

import "testing"

func TestCommandCodeResponsesProtocol(t *testing.T) {
	if commandCodeResponsesProtocol("anthropic/claude-sonnet-4-6") != APIProtocolAnthropic {
		t.Fatal("claude")
	}
	if commandCodeResponsesProtocol("openai/gpt-5.4") != APIProtocolResponses {
		t.Fatal("gpt")
	}
	if commandCodeResponsesProtocol("deepseek/deepseek-v4-flash") != APIProtocolChatCompletions {
		t.Fatal("other")
	}
}

func TestChannelMonitorHealthNeedsRequestSamples(t *testing.T) {
	p50 := int64(100)
	health := ChannelMonitorV2HealthFor(ChannelMonitorV2Metric{
		RequestCount: 1,
		TTFT:         ChannelMonitorV2Latency{SampleCount: 100, P50Ms: &p50},
	})
	if health.Overall != "unknown" {
		t.Fatalf("overall=%s", health.Overall)
	}
}
