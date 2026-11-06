package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelMetadataProviderStub struct {
	enrichFunc   func(ctx context.Context, body []byte, platform string, resolvePricing service.ModelPricingResolverFunc) ([]byte, error)
	syncFunc     func(ctx context.Context) (*service.ModelMetadataSyncResult, error)
	lastPlatform string
	lastPricing  service.ModelPricingResolverFunc
}

func (s *modelMetadataProviderStub) EnrichModelsListBody(ctx context.Context, body []byte, platform string, resolvePricing service.ModelPricingResolverFunc) ([]byte, error) {
	s.lastPlatform = platform
	s.lastPricing = resolvePricing
	if s.enrichFunc != nil {
		return s.enrichFunc(ctx, body, platform, resolvePricing)
	}
	return body, nil
}

func (s *modelMetadataProviderStub) SyncModelMetadata(ctx context.Context) (*service.ModelMetadataSyncResult, error) {
	if s.syncFunc != nil {
		return s.syncFunc(ctx)
	}
	return &service.ModelMetadataSyncResult{Providers: 235, Models: 8934, FetchedAt: "2026-11-07T00:00:00Z"}, nil
}

func testFloatPointer(value float64) *float64 { return &value }

func newModelMetadataTestContext(method, target string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(method, target, nil)
	c.Request = request
	c.Params = params
	return c, recorder
}

func mergeStubCatalogueMetadata(body []byte, modelID, description string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["data"], &entries); err != nil {
		return body, err
	}
	for _, entry := range entries {
		var id string
		_ = json.Unmarshal(entry["id"], &id)
		if id != modelID {
			continue
		}
		entry["description"], _ = json.Marshal(description)
		entry["context_window"], _ = json.Marshal(int64(1_000_000))
	}
	encodedEntries, err := json.Marshal(entries)
	if err != nil {
		return body, err
	}
	envelope["data"] = encodedEntries
	return json.Marshal(envelope)
}

func TestGatewayModelsListMergesCatalogueMetadata(t *testing.T) {
	stub := &modelMetadataProviderStub{
		enrichFunc: func(_ context.Context, body []byte, _ string, _ service.ModelPricingResolverFunc) ([]byte, error) {
			return mergeStubCatalogueMetadata(body, "deepseek-v4-pro", "DeepSeek V4 Pro with million-token context.")
		},
	}
	h := &GatewayHandler{modelMetadata: stub}
	c, recorder := newModelMetadataTestContext(http.MethodGet, "/v1/models", nil)

	h.writeModelsListResponse(c, service.PlatformOpenAI, []openai.Model{{
		ID: "deepseek-v4-pro", Object: "model", Created: 1704067200, OwnedBy: "openai", Type: "model", DisplayName: "deepseek-v4-pro",
	}})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, service.PlatformOpenAI, stub.lastPlatform)

	var response struct {
		Object string `json:"object"`
		Data   []struct {
			ID            string `json:"id"`
			Object        string `json:"object"`
			Created       int64  `json:"created"`
			OwnedBy       string `json:"owned_by"`
			Type          string `json:"type"`
			DisplayName   string `json:"display_name"`
			Description   string `json:"description"`
			ContextWindow int64  `json:"context_window"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "list", response.Object)
	require.Len(t, response.Data, 1)
	entry := response.Data[0]
	require.Equal(t, "deepseek-v4-pro", entry.ID)
	require.Equal(t, "model", entry.Object)
	require.EqualValues(t, 1704067200, entry.Created)
	require.Equal(t, "openai", entry.OwnedBy)
	require.Equal(t, "model", entry.Type)
	require.Equal(t, "DeepSeek V4 Pro with million-token context.", entry.Description)
	require.EqualValues(t, 1_000_000, entry.ContextWindow)
	require.Equal(t, "deepseek-v4-pro", entry.DisplayName)
}

func TestGatewayModelsRetrieveMergesCatalogueMetadata(t *testing.T) {
	stub := &modelMetadataProviderStub{
		enrichFunc: func(_ context.Context, body []byte, _ string, _ service.ModelPricingResolverFunc) ([]byte, error) {
			return mergeStubCatalogueMetadata(body, "deepseek-v4-pro", "DeepSeek V4 Pro.")
		},
	}
	h := &GatewayHandler{modelMetadata: stub}
	c, recorder := newModelMetadataTestContext(http.MethodGet, "/v1/models/deepseek-v4-pro", gin.Params{{Key: "model", Value: "deepseek-v4-pro"}})

	h.writeModelsListResponse(c, service.PlatformOpenAI, []openai.Model{{
		ID: "deepseek-v4-pro", Object: "model", Created: 1704067200, OwnedBy: "openai", Type: "model", DisplayName: "deepseek-v4-pro",
	}})

	require.Equal(t, http.StatusOK, recorder.Code)
	var model map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &model))
	require.Equal(t, "deepseek-v4-pro", model["id"])
	require.Equal(t, "DeepSeek V4 Pro.", model["description"])
	require.EqualValues(t, 1_000_000, model["context_window"])
	require.NotContains(t, model, "data", "retrieve must return the bare model entry")
}

func TestGatewayModelsListMetadataFailureKeepsStandardBody(t *testing.T) {
	stub := &modelMetadataProviderStub{
		enrichFunc: func(_ context.Context, _ []byte, _ string, _ service.ModelPricingResolverFunc) ([]byte, error) {
			return nil, errors.New("registry unavailable")
		},
	}
	h := &GatewayHandler{modelMetadata: stub}
	c, recorder := newModelMetadataTestContext(http.MethodGet, "/v1/models", nil)

	h.writeModelsListResponse(c, service.PlatformOpenAI, []openai.Model{{
		ID: "deepseek-v4-pro", Object: "model", Created: 1704067200, OwnedBy: "openai", Type: "model", DisplayName: "deepseek-v4-pro",
	}})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"object":"list","data":[{"id":"deepseek-v4-pro","object":"model","created":1704067200,"owned_by":"openai","type":"model","display_name":"deepseek-v4-pro"}]}`, recorder.Body.String())
}

func TestGatewayModelsListWithoutMetadataProviderKeepsStandardBody(t *testing.T) {
	c, recorder := newModelMetadataTestContext(http.MethodGet, "/v1/models", nil)
	h := &GatewayHandler{}
	h.writeModelsListResponse(c, service.PlatformOpenAI, []openai.Model{{
		ID: "gpt-image-2", Object: "model", Created: 1704067200, OwnedBy: "openai", Type: "model", DisplayName: "gpt-image-2",
	}})
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, strings.Contains(recorder.Body.String(), `"id":"gpt-image-2"`))
	require.NotContains(t, recorder.Body.String(), `"description"`)
}

func TestGatewayModelsListUsesGroupPriceCard(t *testing.T) {
	apiKey := &service.APIKey{Group: &service.Group{
		ID:       7,
		Platform: service.PlatformOpenAI,
		ModelPricing: []service.ChannelModelPricing{{
			Platform:    service.PlatformOpenAI,
			Models:      []string{"deepseek-v4-pro"},
			BillingMode: service.BillingModeToken,
			InputPrice:  testFloatPointer(0.0000002),
			OutputPrice: testFloatPointer(0.0000008),
		}},
	}}
	var resolved *service.ChannelModelPricing
	stub := &modelMetadataProviderStub{
		enrichFunc: func(_ context.Context, body []byte, _ string, resolvePricing service.ModelPricingResolverFunc) ([]byte, error) {
			if resolvePricing != nil {
				resolved = resolvePricing("deepseek-v4-pro")
			}
			return body, nil
		},
	}
	h := &GatewayHandler{modelMetadata: stub}
	c, recorder := newModelMetadataTestContext(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)

	h.writeModelsListResponse(c, service.PlatformOpenAI, []openai.Model{{
		ID: "deepseek-v4-pro", Object: "model", Created: 1704067200, OwnedBy: "openai", Type: "model", DisplayName: "deepseek-v4-pro",
	}})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, resolved)
	require.InDelta(t, 0.0000002, *resolved.InputPrice, 1e-12)
}

func TestGatewaySyncModelMetadataReturnsStats(t *testing.T) {
	h := &GatewayHandler{modelMetadata: &modelMetadataProviderStub{}}
	c, recorder := newModelMetadataTestContext(http.MethodPost, "/api/v1/admin/models/metadata/sync", nil)

	h.SyncModelMetadata(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int `json:"code"`
		Data struct {
			Providers int    `json:"providers"`
			Models    int    `json:"models"`
			FetchedAt string `json:"fetched_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, 0, response.Code)
	require.Equal(t, 235, response.Data.Providers)
	require.Equal(t, 8934, response.Data.Models)
	require.NotEmpty(t, response.Data.FetchedAt)
}

func TestGatewaySyncModelMetadataReportsUpstreamFailure(t *testing.T) {
	h := &GatewayHandler{modelMetadata: &modelMetadataProviderStub{
		syncFunc: func(context.Context) (*service.ModelMetadataSyncResult, error) {
			return nil, errors.New("registry HTTP 503")
		},
	}}
	c, recorder := newModelMetadataTestContext(http.MethodPost, "/api/v1/admin/models/metadata/sync", nil)

	h.SyncModelMetadata(c)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "registry HTTP 503", "internal error detail must not leak")
}

func TestGatewaySyncModelMetadataUnavailable(t *testing.T) {
	h := &GatewayHandler{}
	c, recorder := newModelMetadataTestContext(http.MethodPost, "/api/v1/admin/models/metadata/sync", nil)
	h.SyncModelMetadata(c)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
