package repository

import (
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupOCRPriceEntityProjection(t *testing.T) {
	for _, price := range []*float64{nil, func() *float64 { v := 0.0; return &v }(), func() *float64 { v := 2.5; return &v }()} {
		group := groupEntityToService(&dbent.Group{ID: 36, OcrPricePer1kPages: price})
		require.Equal(t, price, group.OCRPricePer1kPages)
	}
}

func TestDeepSeekToolsUpstreamProfileHasNoShorterHeaderTimer(t *testing.T) {
	svc := &httpUpstreamService{}
	settings := svc.applyProfilePoolSettings(poolSettings{responseHeaderTimeout: time.Second}, service.HTTPUpstreamProfileDeepSeekTools)
	require.Zero(t, settings.responseHeaderTimeout)
	transport, err := buildUpstreamTransport(settings, nil, upstreamProtocolModeDefault)
	require.NoError(t, err)
	require.Zero(t, transport.ResponseHeaderTimeout)
	transport.CloseIdleConnections()
}
