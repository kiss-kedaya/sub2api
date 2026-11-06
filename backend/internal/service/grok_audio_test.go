package service

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

func TestBuildGrokVoiceURL_UsesAPIDefaultForCLIProxyBase(t *testing.T) {
	account := &Account{
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"base_url": xai.DefaultCLIBaseURL,
		},
	}
	url, err := buildGrokVoiceURL(account, nil, "tts")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/tts", url)

	url, err = buildGrokVoiceURL(account, nil, "realtime")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/realtime", url)
}

func TestBuildGrokVoiceURL_EmptyBaseFallsBackToAPI(t *testing.T) {
	account := &Account{
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{},
	}
	url, err := buildGrokVoiceURL(account, nil, "stt")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/stt", url)
}

func TestBuildGrokVoiceURL_RequiresEndpoint(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	_, err := buildGrokVoiceURL(account, nil, "  ")
	require.Error(t, err)
}

func TestBuildGrokVoiceURL_EncodesCustomVoicePathSegments(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	got, err := buildGrokVoiceURL(account, nil, "custom-voices/nlbqfwie/audio")
	require.NoError(t, err)
	require.Equal(t, xai.DefaultBaseURL+"/custom-voices/nlbqfwie/audio", got)

	_, err = buildGrokVoiceURL(account, nil, "custom-voices/../audio")
	require.Error(t, err)
}

func TestGrokTTSTextPrefersUpstreamTextField(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "text only", body: `{"text":"hello"}`, want: "hello"},
		{name: "mixed prefers text", body: `{"input":"x","text":"0123456789"}`, want: "0123456789"},
		{name: "mixed never overbills input", body: `{"input":"0123456789","text":"x"}`, want: "x"},
		{name: "input fallback", body: `{"input":"short"}`, want: "short"},
		{name: "prompt fallback", body: `{"prompt":"narration"}`, want: "narration"},
		{name: "blank text falls back to input", body: `{"text":"   ","input":"spoken"}`, want: "spoken"},
		{name: "non-string text falls back", body: `{"text":123,"input":"spoken"}`, want: "spoken"},
		{name: "multibyte text", body: `{"text":"你好世界"}`, want: "你好世界"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, GrokTTSText([]byte(tc.body)))
		})
	}
}

func TestEstimateGrokVoiceAudioUsageTTSUsesUpstreamText(t *testing.T) {
	const spoken = "0123456789"
	usage := estimateGrokVoiceAudioUsage("tts", []byte(`{"input":"x","text":"`+spoken+`"}`), "application/json", nil, time.Millisecond)
	require.NotNil(t, usage)
	require.Equal(t, "tts", usage.Mode)
	require.InDelta(t, float64(len(spoken))/1_000_000.0, usage.DurationOrUnits, 1e-12)

	// The inverse mix must not charge the longer compatibility field that xAI
	// never speaks; billing follows "text" in both directions.
	usage = estimateGrokVoiceAudioUsage("tts", []byte(`{"input":"0123456789","text":"x"}`), "application/json", nil, time.Millisecond)
	require.NotNil(t, usage)
	require.InDelta(t, 1/1_000_000.0, usage.DurationOrUnits, 1e-12)
}

func TestForwardGrokVoice_RejectsNonGrok(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokVoice(context.Background(), nil, &Account{Platform: PlatformOpenAI}, "tts", []byte(`{}`), "application/json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}

func TestAwaitGrokRealtimeAudioObservedReadsFlagAfterRelayExits(t *testing.T) {
	errCh := make(chan error, 1)
	var observed atomic.Bool
	go func() {
		observed.Store(true)
		errCh <- io.EOF
	}()
	got, err := awaitGrokRealtimeAudioObserved(errCh, &observed)
	require.ErrorIs(t, err, io.EOF)
	require.True(t, got, "audioObserved must be read after the relay returns, not before <-errCh")
}

func TestGrokRealtimeEventHasAudio(t *testing.T) {
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"session.created"}`)))
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio_transcript.delta","delta":"hi"}`)))
	require.False(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio.delta","delta":""}`)))
	require.True(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.audio.delta","delta":"abc"}`)))
	require.True(t, grokRealtimeEventHasAudio([]byte(`{"type":"response.output_audio.delta","audio":"abc"}`)))
}

func TestForwardGrokVoice_RejectsUnknownEndpoint(t *testing.T) {
	svc := &OpenAIGatewayService{}
	_, err := svc.ForwardGrokVoice(context.Background(), nil, &Account{Platform: PlatformGrok}, "unknown", []byte(`{}`), "application/json")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}
