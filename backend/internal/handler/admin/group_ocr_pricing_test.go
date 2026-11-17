package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupDeepSeekAudioPriceRequestTriState(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"audio_tts_price_per_million_chars":null,"audio_stt_price_per_hour":null}`,
		`{"audio_tts_price_per_million_chars":0,"audio_stt_price_per_hour":0}`,
		`{"audio_tts_price_per_million_chars":45,"audio_stt_price_per_hour":0.116}`,
	} {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(body), &req))
		if body == `{}` {
			require.False(t, req.AudioTtsPricePerMillionChars.set)
			require.False(t, req.AudioSttPricePerHour.set)
		} else {
			require.True(t, req.AudioTtsPricePerMillionChars.set)
			require.True(t, req.AudioSttPricePerHour.set)
		}
		if body == `{}` || body == `{"audio_tts_price_per_million_chars":null,"audio_stt_price_per_hour":null}` {
			require.Nil(t, req.AudioTtsPricePerMillionChars.value)
			require.Nil(t, req.AudioSttPricePerHour.value)
		} else {
			require.NotNil(t, req.AudioTtsPricePerMillionChars.value)
			require.NotNil(t, req.AudioSttPricePerHour.value)
		}
	}
}

func TestGroupOCRPriceRequestTriStateAndDTO(t *testing.T) {
	for _, tc := range []struct {
		body string
		set  bool
		want *float64
	}{
		{`{}`, false, nil},
		{`{"ocr_price_per_1k_pages":null}`, true, nil},
		{`{"ocr_price_per_1k_pages":0}`, true, func() *float64 { v := 0.0; return &v }()},
		{`{"ocr_price_per_1k_pages":2.5}`, true, func() *float64 { v := 2.5; return &v }()},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var req UpdateGroupRequest
			require.NoError(t, json.Unmarshal([]byte(tc.body), &req))
			require.Equal(t, tc.set, req.OCRPricePer1kPages.set)
			require.Equal(t, tc.want, req.OCRPricePer1kPages.value)
			mapped := dto.GroupFromServiceAdmin(&service.Group{OCRPricePer1kPages: tc.want})
			require.Equal(t, tc.want, mapped.OCRPricePer1kPages)
		})
	}
}
