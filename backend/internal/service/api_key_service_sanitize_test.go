//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// api_keys.name 会进密钥列表、通知和邮件；入库必须是纯文本。
func TestCreateAPIKeyStoresSanitizedPlainTextName(t *testing.T) {
	svc, repo, _ := newCreateGroupService()

	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{
		Name: `<img src=x onerror=alert(1)>生产密钥`,
	})
	require.NoError(t, err)
	require.Len(t, repo.created, 1)
	require.Equal(t, "生产密钥", repo.created[0].Name, "落库值必须已剥掉标签")
	require.Equal(t, "生产密钥", key.Name)
}

// 只提交标记时给可读兜底名，避免列表/通知出现空白密钥名。
func TestCreateAPIKeyFallsBackWhenNameIsPayloadOnly(t *testing.T) {
	svc, repo, _ := newCreateGroupService()

	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{
		Name: `<svg onload=alert(1)></svg>`,
	})
	require.NoError(t, err)
	require.Len(t, repo.created, 1)
	require.Equal(t, "API Key", repo.created[0].Name)
	require.Equal(t, "API Key", key.Name)
}

// 名字超长（api_keys.name VARCHAR(100)）在入库前截断，不能把 22001 变成 500。
func TestCreateAPIKeyTruncatesOverlongName(t *testing.T) {
	svc, repo, _ := newCreateGroupService()

	longName := strings.Repeat("名", maxAPIKeyNameRunes+50)
	key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{Name: longName})
	require.NoError(t, err)
	require.Len(t, repo.created, 1)
	require.Len(t, []rune(repo.created[0].Name), maxAPIKeyNameRunes)
	require.Len(t, []rune(key.Name), maxAPIKeyNameRunes)
}

// 编辑路径与创建路径共用 normalizeAPIKeyName：实体编码的标签也要先解码再剥掉。
func TestNormalizeAPIKeyNameSanitizesEncodedMarkup(t *testing.T) {
	cases := map[string]string{
		`&lt;b&gt;主密钥&lt;/b&gt;`:              "主密钥",
		`<a href="javascript:alert(1)">x</a>`: "x",
		"密钥\u200b名称":                          "密钥名称",
		"普通名称":                                "普通名称",
	}
	for raw, want := range cases {
		require.Equal(t, want, normalizeAPIKeyName(raw), "raw=%q", raw)
	}
}
