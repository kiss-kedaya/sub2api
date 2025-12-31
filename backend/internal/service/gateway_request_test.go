package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGatewayRequest(t *testing.T) {
	body := []byte(`{"model":"claude-3-7-sonnet","stream":true,"metadata":{"user_id":"session_123e4567-e89b-12d3-a456-426614174000"},"system":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}],"messages":[{"content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(body)
	require.NoError(t, err)
	require.Equal(t, "claude-3-7-sonnet", parsed.Model)
	require.True(t, parsed.Stream)
	require.Equal(t, "session_123e4567-e89b-12d3-a456-426614174000", parsed.MetadataUserID)
	require.True(t, parsed.HasSystem)
	require.NotNil(t, parsed.System)
	require.Len(t, parsed.Messages, 1)
}

func TestParseGatewayRequest_SystemNull(t *testing.T) {
	body := []byte(`{"model":"claude-3","system":null}`)
	parsed, err := ParseGatewayRequest(body)
	require.NoError(t, err)
	// 显式传入 system:null 也应视为“字段已存在”，避免默认 system 被注入。
	require.True(t, parsed.HasSystem)
	require.Nil(t, parsed.System)
}

func TestParseGatewayRequest_InvalidModelType(t *testing.T) {
	body := []byte(`{"model":123}`)
	_, err := ParseGatewayRequest(body)
	require.Error(t, err)
}

func TestParseGatewayRequest_InvalidStreamType(t *testing.T) {
	body := []byte(`{"stream":"true"}`)
	_, err := ParseGatewayRequest(body)
	require.Error(t, err)
}
