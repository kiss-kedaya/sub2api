package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
)

type excelBPSFixtureState struct {
	scope    string
	payload  []byte
	revision uint64
}

func (s *excelBPSFixtureState) LoadBPSState(_ context.Context, scope, key string) ([]byte, uint64, error) {
	if key == "catalog" && scope == s.scope {
		return append([]byte{}, s.payload...), s.revision, nil
	}
	return nil, 0, nil
}
func (s *excelBPSFixtureState) SaveBPSState(_ context.Context, scope, key string, expected *uint64, raw []byte) (bool, error) {
	s.scope = scope
	s.payload = append([]byte{}, raw...)
	s.revision++
	return true, nil
}
func TestExcelBPSChannelPreservesModelAndUsesIndependentGrant(t *testing.T) {
	s, repo, _, accounts := fixtureExcelGrant(t)
	grant := excelGrantTokens{AccessToken: "excel-token", AccountID: "workspace-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	raw, _ := json.Marshal(grant)
	cipher, err := s.encryptor.Encrypt(string(raw))
	require.NoError(t, err)
	repo.record.Cipher = cipher
	accounts.account.Extra = map[string]any{"excel_bps_enabled": true, "excel_bps_models": []string{"gpt-fixture"}}
	state := &excelBPSFixtureState{}
	channel := NewExcelBPSChannel(s, state)
	prepared, err := channel.Prepare(context.Background(), &accounts.account, []byte(`{"model":"gpt-fixture","input":"hello","stream":true}`), 8, "session-one")
	require.NoError(t, err)
	require.Equal(t, basispoints.ResponsesURL, prepared.Request.URL.String())
	require.Equal(t, "Bearer excel-token", prepared.Request.Header.Get("Authorization"))
	require.Equal(t, "chatgpt", prepared.Request.Header.Get("X-Basispoints-Auth-Mode"))
	require.Equal(t, "workspace-fixture", prepared.Request.Header.Get("X-OpenAI-Account-ID"))
	body, err := io.ReadAll(prepared.Request.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"model":"gpt-fixture"`)
	require.NotContains(t, string(body), "primary-token")
	_, err = channel.Prepare(context.Background(), &accounts.account, []byte(`{"model":"gpt-fixture","input":"hello"}`), 8, "")
	require.Error(t, err)
}
func TestExcelBPSNativeHostedToolsKeepOriginalChannel(t *testing.T) {
	_, _, _, accounts := fixtureExcelGrant(t)
	accounts.account.Extra = map[string]any{"excel_bps_enabled": true, "excel_bps_models": []any{"gpt-fixture"}}
	channel := NewExcelBPSChannel(nil, nil)
	prepared, err := channel.Prepare(context.Background(), &accounts.account, []byte(`{"model":"gpt-fixture","input":"hello","tools":[{"type":"image_generation"}]}`), 8, "session")
	require.NoError(t, err)
	require.True(t, prepared.NativeFallback)
	require.Nil(t, prepared.Request)
}
func TestExcelBPSTransformKeepsHTTPFailureAndStreamsText(t *testing.T) {
	_, bridge, err := basispoints.Prepare([]byte(`{"model":"gpt-fixture","input":"hello","stream":true}`), "fixture", nil)
	require.NoError(t, err)
	prepared := &ExcelBPSPrepared{Bridge: bridge}
	failed := &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"denied"}`))}
	actual, err := prepared.Transform(context.Background(), failed)
	require.NoError(t, err)
	require.Same(t, failed, actual)
	require.Equal(t, 403, actual.StatusCode)
	stream := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"hello"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_fixture","model":"gpt-fixture","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":1}}}

`
	actual, err = prepared.Transform(context.Background(), &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))})
	require.NoError(t, err)
	defer func() { _ = actual.Body.Close() }()
	body, err := io.ReadAll(actual.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"delta":"hello"`)
	require.Contains(t, string(body), "response.completed")
	require.Contains(t, string(body), `"input_tokens":3`)
}
