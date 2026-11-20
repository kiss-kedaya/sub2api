package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// anthropicThinkingRewriteRepo 是网关/设置用例共享的最小内存 SettingRepository：
// 已显式配置的 key 读得出，其余按未配置处理。
type anthropicThinkingRewriteRepo struct {
	values map[string]string
	err    error
}

var _ SettingRepository = (*anthropicThinkingRewriteRepo)(nil)

func (r *anthropicThinkingRewriteRepo) Get(_ context.Context, _ string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *anthropicThinkingRewriteRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (r *anthropicThinkingRewriteRepo) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

func (r *anthropicThinkingRewriteRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if r.err != nil {
		return out, r.err
	}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *anthropicThinkingRewriteRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *anthropicThinkingRewriteRepo) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *anthropicThinkingRewriteRepo) Delete(_ context.Context, key string) error {
	delete(r.values, key)
	return nil
}

func newAnthropicThinkingRewriteService(t *testing.T, repo SettingRepository) *SettingService {
	t.Helper()
	return NewSettingService(repo, &config.Config{})
}

func rectifierSettingsJSON(t *testing.T, settings RectifierSettings) string {
	t.Helper()
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	return string(raw)
}

// anthropicThinkingRewriteSamples 是三组会被 sanitizeAnthropicThinkingForModel
// 改动的请求体；开关关闭时它们必须字节级完全不变。
var anthropicThinkingRewriteSamples = []struct {
	name    string
	modelID string
	body    string
}{
	{
		name:    "disabled",
		modelID: "claude-opus-5-5",
		body: "{\n  \"model\": \"claude-opus-5-5\",\n  \"thinking\": {\"type\": \"disabled\"},\n  " +
			"\"messages\": [{\"role\": \"user\", \"content\": \"hi\"}]\n}",
	},
	{
		name:    "enabled_budget_tokens",
		modelID: "claude-opus-5-5",
		body: "{\"model\":\"claude-opus-5-5\",\"thinking\":{\"type\":\"enabled\",\"budget_tokens\":2048}," +
			"\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
	},
	{
		name:    "between_tools",
		modelID: "claude-opus-5-5",
		body: "{\"model\":\"claude-opus-5-5\",\"thinking\":{\"type\":\"between_tools\"}," +
			"\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
	},
}

// 默认关闭（设置缺省）时，三组样本必须一个字节都不改。
func TestAnthropicThinkingRewriteDisabledByDefaultKeepsBodyByteIdentical(t *testing.T) {
	t.Parallel()
	require.False(t, DefaultRectifierSettings().AnthropicThinkingRewriteEnabled,
		"默认配置必须关闭 Anthropic thinking 改写")

	svc := newAnthropicThinkingRewriteService(t, &anthropicThinkingRewriteRepo{})

	for _, sample := range anthropicThinkingRewriteSamples {
		sample := sample
		t.Run(sample.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			body := []byte(sample.body)

			// 先证明这组样本确实会被改写函数改动，避免「本来就不变」的空测试。
			require.NotEqual(t, string(body), string(sanitizeAnthropicThinkingForModel(body, sample.modelID)),
				"样本必须能被 sanitizeAnthropicThinkingForModel 改动，否则测不出开关")

			require.False(t, svc.IsAnthropicThinkingRewriteEnabled(ctx))
			got := svc.RewriteAnthropicThinkingForModel(ctx, body, sample.modelID)
			require.Equal(t, body, got, "关闭时必须逐字节原样返回")
		})
	}
}

// 打开后行为与改动前的 thinking gate 完全一致（沿用既有断言）。
func TestAnthropicThinkingRewriteEnabledMatchesSanitizer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := &anthropicThinkingRewriteRepo{values: map[string]string{
		SettingKeyRectifierSettings: rectifierSettingsJSON(t, RectifierSettings{
			Enabled:                         true,
			ThinkingSignatureEnabled:        true,
			ThinkingBudgetEnabled:           true,
			AnthropicThinkingRewriteEnabled: true,
		}),
	}}
	svc := newAnthropicThinkingRewriteService(t, repo)
	require.True(t, svc.IsAnthropicThinkingRewriteEnabled(ctx))

	for _, sample := range anthropicThinkingRewriteSamples {
		sample := sample
		t.Run(sample.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(sample.body)
			want := sanitizeAnthropicThinkingForModel(body, sample.modelID)
			require.NotEqual(t, string(body), string(want))
			require.Equal(t, string(want), string(svc.RewriteAnthropicThinkingForModel(ctx, body, sample.modelID)))
		})
	}

	// disabled：整段删除 thinking。
	disabledOut := svc.RewriteAnthropicThinkingForModel(ctx, []byte(anthropicThinkingRewriteSamples[0].body), "claude-opus-5-5")
	require.False(t, gjson.GetBytes(disabledOut, "thinking").Exists())

	// enabled + budget_tokens：转 adaptive 并删掉 budget_tokens。
	enabledOut := svc.RewriteAnthropicThinkingForModel(ctx, []byte(anthropicThinkingRewriteSamples[1].body), "claude-opus-5-5")
	require.Equal(t, "adaptive", gjson.GetBytes(enabledOut, "thinking.type").String())
	require.False(t, gjson.GetBytes(enabledOut, "thinking.budget_tokens").Exists())

	// between_tools：opus 5.5 不接受，转 adaptive。
	betweenToolsOpus := svc.RewriteAnthropicThinkingForModel(ctx, []byte(anthropicThinkingRewriteSamples[2].body), "claude-opus-5-5")
	require.Equal(t, "adaptive", gjson.GetBytes(betweenToolsOpus, "thinking.type").String())

	// between_tools：sonnet 5.5 上游合法，保持原样。
	sonnetBody := []byte(strings.ReplaceAll(anthropicThinkingRewriteSamples[2].body, "claude-opus-5-5", "claude-sonnet-5-5"))
	require.Equal(t, string(sonnetBody),
		string(svc.RewriteAnthropicThinkingForModel(ctx, sonnetBody, "claude-sonnet-5-5")))
}

// 本子开关独立控制：只看 anthropic_thinking_rewrite_enabled，不叠加整流器总开关。
// 默认 false 即全量原样透传；显式打开才改写，不引入额外限制。
func TestAnthropicThinkingRewriteSubSwitchIsAuthoritative(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// 总开关关闭、子开关打开：仍然改写。
	repoOn := &anthropicThinkingRewriteRepo{values: map[string]string{
		SettingKeyRectifierSettings: rectifierSettingsJSON(t, RectifierSettings{
			Enabled:                         false,
			AnthropicThinkingRewriteEnabled: true,
		}),
	}}
	svcOn := newAnthropicThinkingRewriteService(t, repoOn)
	require.True(t, svcOn.IsAnthropicThinkingRewriteEnabled(ctx))
	body := []byte(anthropicThinkingRewriteSamples[1].body)
	require.NotEqual(t, string(body),
		string(svcOn.RewriteAnthropicThinkingForModel(ctx, body, "claude-opus-5-5")))

	// 总开关打开、子开关关闭：仍原样透传。
	repoOff := &anthropicThinkingRewriteRepo{values: map[string]string{
		SettingKeyRectifierSettings: rectifierSettingsJSON(t, RectifierSettings{
			Enabled:                         true,
			AnthropicThinkingRewriteEnabled: false,
		}),
	}}
	svcOff := newAnthropicThinkingRewriteService(t, repoOff)
	require.False(t, svcOff.IsAnthropicThinkingRewriteEnabled(ctx))
	require.Equal(t, body, svcOff.RewriteAnthropicThinkingForModel(ctx, body, "claude-opus-5-5"))
}

// 读配置失败必须 fail-closed：宁可原样透传，也不能偷偷改写。
func TestAnthropicThinkingRewriteFailsClosedOnRepositoryError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc := newAnthropicThinkingRewriteService(t, &anthropicThinkingRewriteRepo{err: errors.New("db down")})
	require.False(t, svc.IsAnthropicThinkingRewriteEnabled(ctx))

	body := []byte(anthropicThinkingRewriteSamples[1].body)
	require.Equal(t, body, svc.RewriteAnthropicThinkingForModel(ctx, body, "claude-opus-5-5"))
}

// 设置读写：默认 false，保存后生效并从存储读回 true。
func TestRectifierSettingsAnthropicThinkingRewritePersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := &anthropicThinkingRewriteRepo{}
	svc := newAnthropicThinkingRewriteService(t, repo)

	settings, err := svc.GetRectifierSettings(ctx)
	require.NoError(t, err)
	require.False(t, settings.AnthropicThinkingRewriteEnabled, "未保存过时必须默认关闭")

	require.NoError(t, svc.SetRectifierSettings(ctx, &RectifierSettings{
		Enabled:                         true,
		ThinkingSignatureEnabled:        true,
		ThinkingBudgetEnabled:           true,
		AnthropicThinkingRewriteEnabled: true,
	}))
	require.True(t, svc.IsAnthropicThinkingRewriteEnabled(ctx))

	// 新的 service 实例从同一份存储读回，模拟重启/多实例。
	reloaded, err := newAnthropicThinkingRewriteService(t, repo).GetRectifierSettings(ctx)
	require.NoError(t, err)
	require.True(t, reloaded.AnthropicThinkingRewriteEnabled)

	var stored RectifierSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyRectifierSettings]), &stored))
	require.True(t, stored.AnthropicThinkingRewriteEnabled, "持久化 JSON 必须带上新字段")

	// 再关回去同样生效。
	require.NoError(t, svc.SetRectifierSettings(ctx, &RectifierSettings{
		Enabled:                         true,
		AnthropicThinkingRewriteEnabled: false,
	}))
	require.False(t, svc.IsAnthropicThinkingRewriteEnabled(ctx))
}
