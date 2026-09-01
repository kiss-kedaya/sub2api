package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildOpenAIWSCreatePayloadRaw_PreservesLargeValuesAndEnvelopeSemantics(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	raw := []byte(` {"model":"gpt-5.1","type":"response.create","stream":false,"background":true,"input":[{"type":"input_text","text":"keep exact"}],"opaque":{"n":9007199254740993}} `)

	updated, err := svc.buildOpenAIWSCreatePayloadRaw(raw, account)
	require.NoError(t, err)
	require.Equal(t, false, gjson.GetBytes(updated, "stream").Bool())
	require.False(t, gjson.GetBytes(updated, "background").Exists())
	require.Equal(t, "response.create", gjson.GetBytes(updated, "type").String())
	require.Equal(t, "keep exact", gjson.GetBytes(updated, "input.0.text").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(updated, "opaque.n").String())
}

func TestBuildOpenAIWSCreatePayloadRaw_OAuthForcesStoreFalse(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	updated, err := svc.buildOpenAIWSCreatePayloadRaw([]byte(`{"model":"gpt-5.1","store":true}`), account)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "store").Bool())
	require.True(t, gjson.GetBytes(updated, "stream").Bool())
}

func TestPrepareOpenAIWSForwardPayload_RawMatchesMapSemantics(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		account  *Account
		metadata string
		attempt  int
	}{
		{
			name:     "api key envelope and metadata",
			body:     `{"model":"gpt-5.1","background":true,"input":[{"text":"hello"}],"client_metadata":{"keep":"yes"}}`,
			account:  &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			metadata: `{"trace":"1"}`,
			attempt:  1,
		},
		{
			name:    "oauth retry drops include and forces store",
			body:    `{"model":"gpt-5.1","store":true,"include":["reasoning.encrypted_content"],"input":[{"text":"hello"}]}`,
			account: &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
			attempt: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqBody map[string]any
			require.NoError(t, decodeOpenAIJSONUseNumber([]byte(tc.body), &reqBody))
			want := (&OpenAIGatewayService{}).buildOpenAIWSCreatePayload(reqBody, tc.account)
			wantStrategy, wantRemoved := applyOpenAIWSRetryPayloadStrategy(want, tc.attempt)
			setOpenAIWSTurnMetadata(want, tc.metadata)
			wantBytes, err := json.Marshal(want)
			require.NoError(t, err)

			gotBytes, gotStrategy, gotRemoved, usedRaw, err := (&OpenAIGatewayService{}).prepareOpenAIWSForwardPayload(
				reqBody, []byte(tc.body), tc.account, tc.attempt, tc.metadata, nil,
			)
			require.NoError(t, err)
			require.True(t, usedRaw)
			require.Equal(t, wantStrategy, gotStrategy)
			require.Equal(t, wantRemoved, gotRemoved)
			wantNormalized, err := normalizeOpenAIWSJSONForCompare(wantBytes)
			require.NoError(t, err)
			gotNormalized, err := normalizeOpenAIWSJSONForCompare(gotBytes)
			require.NoError(t, err)
			require.Equal(t, wantNormalized, gotNormalized)
		})
	}
}

func TestPrepareOpenAIWSForwardPayload_InvalidRawFallsBackToMap(t *testing.T) {
	reqBody := map[string]any{
		"model": "gpt-5.1",
		"input": []any{map[string]any{"text": "keep"}},
	}

	got, _, _, usedRaw, err := (&OpenAIGatewayService{}).prepareOpenAIWSForwardPayload(
		reqBody, []byte(`{"model":`), &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, 1, "", nil,
	)
	require.NoError(t, err)
	require.False(t, usedRaw)
	require.Equal(t, "response.create", gjson.GetBytes(got, "type").String())
	require.Equal(t, "keep", gjson.GetBytes(got, "input.0.text").String())
}

func TestSetOpenAIWSTurnMetadataRaw_PreservesExistingMetadata(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.1","client_metadata":{"keep":"yes","x-codex-turn-metadata":"old"},"input":[{"opaque":9007199254740993}]}`)
	updated, changed, err := setOpenAIWSTurnMetadataRaw(raw, ` {"trace":"new"} `)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "yes", gjson.GetBytes(updated, "client_metadata.keep").String())
	require.Equal(t, `{"trace":"new"}`, gjson.GetBytes(updated, "client_metadata.x-codex-turn-metadata").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(updated, "input.0.opaque").String())
}

func TestSetOpenAIWSTurnMetadataRaw_ReplacesNonObjectMetadata(t *testing.T) {
	for _, existing := range []string{"null", `"legacy"`, `[]`} {
		raw := []byte(`{"client_metadata":` + existing + `,"model":"gpt-5.1"}`)
		updated, changed, err := setOpenAIWSTurnMetadataRaw(raw, "turn-1")
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "turn-1", gjson.GetBytes(updated, "client_metadata.x-codex-turn-metadata").String())
		require.Equal(t, "gpt-5.1", gjson.GetBytes(updated, "model").String())
	}
}

func TestApplyOpenAIWSRetryPayloadStrategyRaw_OnlyDropsInclude(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.1","include":["reasoning.encrypted_content"],"prompt_cache_key":"stable","input":[{"text":"large"}]}`)
	updated, strategy, removed, err := applyOpenAIWSRetryPayloadStrategyRaw(raw, 2)
	require.NoError(t, err)
	require.Equal(t, "trim_optional_fields", strategy)
	require.Equal(t, []string{"include"}, removed)
	require.False(t, gjson.GetBytes(updated, "include").Exists())
	require.Equal(t, "stable", gjson.GetBytes(updated, "prompt_cache_key").String())
	require.Equal(t, "large", gjson.GetBytes(updated, "input.0.text").String())
}

func TestWriteOpenAIWSJSON_UsesRawWriterWithoutMarshal(t *testing.T) {
	conn := &rawCaptureWSConn{}
	raw := json.RawMessage(`{"model":"gpt-5.1","n":9007199254740993}`)
	require.NoError(t, writeOpenAIWSJSON(nil, conn, raw))
	require.Equal(t, []byte(raw), conn.payload)
	require.Equal(t, "9007199254740993", gjson.GetBytes(conn.payload, "n").String())
}

func TestWriteOpenAIWSJSON_InvalidRawUsesJSONFallback(t *testing.T) {
	conn := &rawCaptureWSConn{}
	err := writeOpenAIWSJSON(context.Background(), conn, json.RawMessage(`{"model":`))
	require.ErrorIs(t, err, errRawCaptureJSONFallback)
	require.Empty(t, conn.payload)
}

type rawCaptureWSConn struct {
	payload []byte
}

var errRawCaptureJSONFallback = errors.New("raw writer should not accept invalid JSON")

func (c *rawCaptureWSConn) WriteJSON(_ context.Context, _ any) error {
	return errRawCaptureJSONFallback
}

func (c *rawCaptureWSConn) WriteFrame(_ context.Context, _ coderws.MessageType, payload []byte) error {
	c.payload = append(c.payload[:0], payload...)
	return nil
}

func (*rawCaptureWSConn) ReadMessage(context.Context) ([]byte, error) { return nil, io.EOF }
func (*rawCaptureWSConn) Ping(context.Context) error                  { return nil }
func (*rawCaptureWSConn) Close() error                                { return nil }
