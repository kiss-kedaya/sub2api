package repository

import (
	"context"
	"encoding/json"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountCustomUsageLockedEditPreservesCurrentManagedFields(t *testing.T) {
	for _, configured := range []bool{true, false} {
		t.Run(map[bool]string{true: "concurrent rotation", false: "removed fields cannot be resurrected"}[configured], func(t *testing.T) {
			database, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, database)))
			t.Cleanup(func() { _ = client.Close() })
			currentExtra := []byte(`{}`)
			var currentSecrets []byte
			if configured {
				currentExtra = []byte(`{"custom_usage_config":{"enabled":true,"request":{"url":"{{baseUrl}}/rotated"}}}`)
				currentSecrets = []byte(`{"api_key":"new-private-key"}`)
			}
			account := &service.Account{ID: 27, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "gateway-key", service.CustomUsageCredentialsKey: map[string]any{"api_key": "old-private-key"}},
				Extra:       map[string]any{"ordinary_setting": true, service.CustomUsageExtraKey: map[string]any{"enabled": false}},
			}
			payload, err := json.Marshal(account.Credentials)
			require.NoError(t, err)
			mock.ExpectQuery(`(?s)SELECT.*credentials -> 'custom_usage_secrets'.*FOR NO KEY UPDATE`).
				WithArgs(int64(27), service.PlatformOpenAI, service.AccountTypeAPIKey, string(payload), nil).
				WillReturnRows(sqlmock.NewRows([]string{"identity", "ollama_identity", "proxy", "enabled", "rate_sync", "snapshot", "session", "auto", "ollama_snapshot", "current_extra", "custom_usage_secrets"}).
					AddRow(false, false, true, nil, nil, nil, nil, nil, nil, currentExtra, currentSecrets))
			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			require.Equal(t, true, extra["ordinary_setting"])
			require.Equal(t, "gateway-key", account.Credentials["api_key"])
			if configured {
				require.Equal(t, map[string]any{"enabled": true, "request": map[string]any{"url": "{{baseUrl}}/rotated"}}, extra[service.CustomUsageExtraKey])
				require.Equal(t, map[string]any{"api_key": "new-private-key"}, account.Credentials[service.CustomUsageCredentialsKey])
			} else {
				require.NotContains(t, extra, service.CustomUsageExtraKey)
				require.NotContains(t, account.Credentials, service.CustomUsageCredentialsKey)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
