package atrestmigrate

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/stretchr/testify/require"
)

// 迁移器为了不把整棵 repository 依赖带进 CLI 以外的地方仍需导入 repository
// 的白名单常量，但封存投影是独立实现；这里强制两处实现输出完全一致，
// 否则迁移写出的封存 jsonb 会永远匹配不上 repository 的 CAS 谓词。
func TestSealedProjectionMatchesRepositorySeal(t *testing.T) {
	credentials := map[string]any{
		"api_key":              "sk-abc",
		"access_token":         " at-abc ",
		"refresh_token":        "rt",
		"id_token":             "id",
		"session_key":          "sess",
		"cookie":               "c=1",
		"custom_usage_secrets": map[string]any{"api_key": "nested"},
		"base_url":             "https://api.example.com",
		"model_mapping":        map[string]any{"gpt": "gpt-4"},
		"oauth_type":           "openai",
		"email":                "user@example.com",
	}
	migratorJSON, err := marshalSealed(credentials)
	require.NoError(t, err)
	repositoryJSON, err := json.Marshal(repository.SealCredentials(credentials))
	require.NoError(t, err)
	require.JSONEq(t, string(repositoryJSON), migratorJSON)
}
