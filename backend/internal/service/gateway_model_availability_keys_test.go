//go:build unit

package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// 可用模型键必须是账号映射键的完整集合：分组白名单过滤发生在 handler，
// 若在这里先截断到前 N 个，白名单里排在后面的模型会被误丢。
func TestCollectAvailableModelKeys_NotCappedSortedDeduped(t *testing.T) {
	accounts := make([]Account, 0, 3)
	for acc := 0; acc < 3; acc++ {
		mapping := map[string]any{"shared": "shared"}
		for i := 0; i < 10; i++ {
			mapping[fmt.Sprintf("model-%02d", acc*10+i)] = "upstream"
		}
		accounts = append(accounts, Account{ID: int64(acc + 1), Credentials: map[string]any{"model_mapping": mapping}})
	}

	keys := collectAvailableModelKeys(accounts)

	require.Len(t, keys, 31, "30 distinct keys + the shared one, no truncation")
	require.Equal(t, "model-00", keys[0])
	require.Equal(t, "shared", keys[len(keys)-1])
	require.IsNonDecreasing(t, keys)
}
