package service

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomUsageCookieValuesNeverReachResultsOrCache(t *testing.T) {
	for _, header := range []string{"session=dummy-private-session; preference=dark", `session="dummy-private-session"; preference=dark`, "session=first-private; session=dummy-private-session"} {
		for _, field := range []string{"plan_name", "unit"} {
			t.Run(header+"/"+field, func(t *testing.T) {
				usage, _, config := customUsageFixture(t)
				config.Request.Headers = map[string]string{"cOoKiE": header}
				if field == "plan_name" {
					config.Extractor.PlanName = &CustomUsageText{Path: "echo"}
				} else {
					config.Extractor.Unit = &CustomUsageText{Path: "echo"}
					config.Extractor.Unit.Value = ""
				}
				_, err := usage.PutConfig(t.Context(), 1, config)
				require.NoError(t, err)
				calls := 0
				usage.client.Transport = customUsageRoundTrip(func(request *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, header, request.Header.Get("Cookie"))
					return customUsageResponse(`{"data":{"remaining":1},"echo":"dummy-private-session"}`, http.StatusOK), nil
				})
				result, err := usage.Query(t.Context(), 1, true, nil)
				require.NoError(t, err)
				require.Equal(t, "invalid_response", result.Error)
				require.Nil(t, result.Remaining)
				require.Nil(t, result.UpdatedAt)
				items, err := usage.Batch(t.Context(), []int64{1})
				require.NoError(t, err)
				encoded, err := json.Marshal(items)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "dummy-private-session")
				require.Empty(t, items[1].PlanName)
				require.Empty(t, items[1].Unit)
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestCustomUsageExpandedRequestLimits(t *testing.T) {
	for _, scenario := range []string{"single_header", "total_headers", "url", "escaped_url"} {
		t.Run(scenario, func(t *testing.T) {
			usage, repo, config := customUsageFixture(t)
			config.APIKey = strings.Repeat("x", 8192)
			switch scenario {
			case "single_header":
				config.Request.Headers = map[string]string{"X-Usage": strings.Repeat("{{apiKey}}", 800)}
			case "total_headers":
				config.Request.Headers = map[string]string{"X-One": "{{apiKey}}", "X-Two": "{{apiKey}}", "X-Three": "{{apiKey}}", "X-Four": "{{apiKey}}"}
			case "url":
				config.Request.Headers = nil
				config.Request.URL = "{{baseUrl}}/balance?key=" + strings.Repeat("{{apiKey}}", 300)
			case "escaped_url":
				config.APIKey = strings.Repeat("/", 3000)
				config.Request.Headers = nil
				config.Request.URL = "{{baseUrl}}/balance?key={{apiKey}}"
			}
			encoded, err := json.Marshal(config)
			require.NoError(t, err)
			require.Less(t, len(encoded), 32*1024)
			merged, secrets := mergeCustomUsage(config, customUsageDefaults(), customUsageSecrets{})
			prepared, err := prepareCustomUsage(repo.accounts[1], merged, secrets)
			require.ErrorIs(t, err, ErrCustomUsageConfig)
			require.Nil(t, prepared.request)
			_, err = usage.PutConfig(t.Context(), 1, config)
			require.ErrorIs(t, err, ErrCustomUsageConfig)
			require.Zero(t, repo.writes)
			usage.client.Transport = customUsageRoundTrip(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid expansion must not reach upstream")
				return nil, ErrCustomUsageConfig
			})
			result, err := usage.Query(t.Context(), 1, true, &config)
			require.NoError(t, err)
			require.Equal(t, "invalid_config", result.Error)
			require.Nil(t, result.Remaining)
		})
	}
}

func TestCustomUsageInterpolationIsNotRecursive(t *testing.T) {
	_, repo, config := customUsageFixture(t)
	config.Request.Headers = map[string]string{"X-Usage": "{{apiKey}}"}
	config.Request.URL = "{{baseUrl}}/balance?key={{apiKey}}"
	prepared, err := prepareCustomUsage(repo.accounts[1], config, customUsageSecrets{APIKey: "literal-{{accessToken}}", AccessToken: "must-not-interpolate"})
	require.NoError(t, err)
	require.Equal(t, "literal-{{accessToken}}", prepared.request.Header.Get("X-Usage"))
	require.Equal(t, "literal-{{accessToken}}", prepared.request.URL.Query().Get("key"))
	require.NotContains(t, prepared.request.Header.Get("X-Usage"), "must-not-interpolate")
}

func TestCustomUsageExpansionPreflight(t *testing.T) {
	for _, value := range []string{"plain", "-_.~ /+?=&%", "literal-{{accessToken}}", "额度", string([]byte{0, 127, 128, 255})} {
		for _, query := range []bool{false, true} {
			t.Run(value+map[bool]string{true: "/query", false: "/header"}[query], func(t *testing.T) {
				variables := map[string]string{"apiKey": value, "accessToken": "never-substitute"}
				expected := value
				if query {
					expected = url.QueryEscape(value)
				}
				expected = "before:" + expected + ":after:" + expected
				template := "before:{{apiKey}}:after:{{apiKey}}"
				length, err := customUsageExpandedLength(template, variables, query, len(expected))
				require.NoError(t, err)
				require.Equal(t, len(expected), length)
				expanded, err := customUsageExpand(template, variables, query, len(expected))
				require.NoError(t, err)
				require.Equal(t, expected, expanded)
				expanded, err = customUsageExpand(template, variables, query, len(expected)-1)
				require.ErrorIs(t, err, ErrCustomUsageConfig)
				require.Empty(t, expanded)
			})
		}
	}
	variables := map[string]string{"apiKey": strings.Repeat("x", 8192)}
	template := strings.Repeat("{{apiKey}}", 800)
	var expansionError error
	allocations := testing.AllocsPerRun(100, func() {
		_, expansionError = customUsageExpand(template, variables, false, customUsageMaxHeaderValueBytes)
	})
	require.ErrorIs(t, expansionError, ErrCustomUsageConfig)
	require.Zero(t, allocations, "rejected expansion must not allocate its output")
}

func TestCustomUsageCookieValidationAndNormalResponse(t *testing.T) {
	for _, header := range []string{"session", `session="unterminated`, `session=invalid\value`} {
		t.Run(header, func(t *testing.T) {
			_, repo, config := customUsageFixture(t)
			config.Request.Headers = map[string]string{"Cookie": header}
			prepared, err := prepareCustomUsage(repo.accounts[1], config, customUsageSecrets{})
			require.ErrorIs(t, err, ErrCustomUsageConfig)
			require.Nil(t, prepared.request)
		})
	}
	_, repo, config := customUsageFixture(t)
	config.Request.Headers = map[string]string{"Cookie": "session=private-cookie; empty="}
	config.Extractor.PlanName = &CustomUsageText{Path: "plan"}
	prepared, err := prepareCustomUsage(repo.accounts[1], config, customUsageSecrets{})
	require.NoError(t, err)
	result, err := parseCustomUsage([]byte(`{"data":{"remaining":2},"plan":"standard"}`), prepared.config, prepared.sensitive)
	require.NoError(t, err)
	require.Equal(t, "standard", result.PlanName)
	require.Equal(t, "USD", result.Unit)
	require.Equal(t, 2.0, *result.Remaining)
}

func TestCustomUsageRequestLimitBoundaries(t *testing.T) {
	for _, scenario := range []string{"single_header", "total_headers", "url"} {
		for _, excess := range []int{0, 1} {
			t.Run(scenario+strings.Repeat("+", excess), func(t *testing.T) {
				_, repo, config := customUsageFixture(t)
				secrets := customUsageSecrets{}
				switch scenario {
				case "single_header":
					secrets.APIKey = strings.Repeat("x", customUsageMaxHeaderValueBytes)
					config.Request.Headers = map[string]string{"X-Usage": strings.Repeat("x", excess) + "{{apiKey}}"}
				case "total_headers":
					config.Request.Headers = map[string]string{"X-One": "{{apiKey}}", "X-Two": "{{apiKey}}", "X-Three": "{{apiKey}}", "X-Four": "{{accessToken}}"}
					secrets.APIKey = strings.Repeat("x", 8192)
					baseBytes := len("Accept: application/json\r\nHost: \r\nUser-Agent: Go-http-client/1.1\r\nConnection: close\r\n\r\n") + len("billing.example.com")
					for name := range config.Request.Headers {
						baseBytes += len(name) + 4
					}
					secrets.AccessToken = strings.Repeat("y", customUsageMaxHeaderBytes-baseBytes-3*8192+excess)
				case "url":
					config.Request.Headers = nil
					config.Request.URL = "{{baseUrl}}/balance?key={{apiKey}}"
					secrets.APIKey = strings.Repeat("x", customUsageMaxURLBytes-len("https://billing.example.com/balance?key=")+excess)
				}
				prepared, err := prepareCustomUsage(repo.accounts[1], config, secrets)
				if excess > 0 {
					require.ErrorIs(t, err, ErrCustomUsageConfig)
					require.Nil(t, prepared.request)
				} else {
					require.NoError(t, err)
					require.NotNil(t, prepared.request)
				}
			})
		}
	}
}
