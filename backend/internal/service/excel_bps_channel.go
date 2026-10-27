package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/tidwall/gjson"
)

type ExcelBPSChannel struct {
	grants *ExcelGrantService
	state  basispoints.StateStore
}

func NewExcelBPSChannel(grants *ExcelGrantService, state basispoints.StateStore) *ExcelBPSChannel {
	return &ExcelBPSChannel{grants, state}
}

// ExcelBPSRouteSelected reads only the selected account's scheduler snapshot.
// Disabled/ordinary accounts do not consult PostgreSQL in the request path.
func ExcelBPSRouteSelected(a *Account, model string) bool {
	if !credentialGuardEligible(a) || a.Extra == nil {
		return false
	}
	enabled, _ := a.Extra["excel_bps_enabled"].(bool)
	if !enabled {
		return false
	}
	switch models := a.Extra["excel_bps_models"].(type) {
	case []string:
		for _, value := range models {
			if value == model {
				return true
			}
		}
	case []any:
		for _, value := range models {
			if value == model {
				return true
			}
		}
	}
	return false
}

type ExcelBPSPrepared struct {
	Request        *http.Request
	Bridge         *basispoints.Bridge
	NativeFallback bool
	Warnings       []string
}

// Prepare returns a request for the existing gateway transport. It never picks
// an account or records billing. Caller keeps the existing account/proxy/usage
// path, and wraps a successful SSE body with Transform before normal relay.
func (s *ExcelBPSChannel) Prepare(ctx context.Context, a *Account, raw []byte, apiKeyID int64, session string) (*ExcelBPSPrepared, error) {
	model := gjson.GetBytes(raw, "model").String()
	if !ExcelBPSRouteSelected(a, model) {
		return nil, errors.New("BPS route is not enabled for this account and model")
	}
	if basispoints.NativeFallbackReason(raw) != "" {
		return &ExcelBPSPrepared{NativeFallback: true}, nil
	}
	if apiKeyID <= 0 || strings.TrimSpace(session) == "" || len(session) > 4096 {
		return nil, errors.New("BPS requests require a stable authenticated conversation identity")
	}
	if s.state == nil {
		return nil, basispoints.ErrStateUnavailable
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(a.ID, 10) + ":" + strconv.FormatInt(apiKeyID, 10) + ":" + session))
	scope := hex.EncodeToString(sum[:])
	replay, catalog := basispoints.NewPersistentCaches(ctx, s.state)
	body, bridge, err := basispoints.PrepareWithCatalog(raw, scope, replay, catalog)
	if err != nil {
		return nil, err
	}
	token, accountID, err := s.grants.AccessToken(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, basispoints.ResponsesURL, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot create BPS request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("X-OpenAI-Account-ID", accountID)
	req.Header.Set("X-Basispoints-Auth-Mode", "chatgpt")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Origin", "https://bps.openai.com")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Product", "basispoints-excel-plugin")
	req.Header.Set("X-OpenAI-Internal-Basispoints-Client-Agent-Profile", "excel")
	return &ExcelBPSPrepared{Request: req, Bridge: bridge, Warnings: append([]string{}, bridge.Warnings...)}, nil
}
func (p *ExcelBPSPrepared) Transform(ctx context.Context, response *http.Response) (*http.Response, error) {
	if response == nil {
		return nil, errors.New("BPS upstream returned no response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || p.NativeFallback {
		return response, nil
	}
	if p.Bridge == nil {
		return nil, errors.New("BPS bridge unavailable")
	}
	copy := *response
	copy.Header = response.Header.Clone()
	copy.Header.Set("Content-Type", "text/event-stream")
	copy.Header.Del("Content-Length")
	copy.ContentLength = -1
	copy.Body = p.Bridge.StreamWithRepairs(ctx, response.Body, nil, nil)
	return &copy, nil
}
