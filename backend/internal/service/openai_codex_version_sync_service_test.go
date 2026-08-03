package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- mock: 只实现同步任务用到的读写，其余方法不应被调用 ---

type codexVersionSyncSettingRepoStub struct {
	SettingRepository // 嵌入接口，未实现的方法会 panic（不应被调用）

	mu        sync.Mutex
	values    map[string]string
	getErr    error
	setErr    error
	updatedAt time.Time
	writes    []string
}

func newCodexVersionSyncSettingRepoStub(values map[string]string) *codexVersionSyncSettingRepoStub {
	if values == nil {
		values = map[string]string{}
	}
	return &codexVersionSyncSettingRepoStub{values: values}
}

func (r *codexVersionSyncSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return "", r.getErr
	}
	return r.values[key], nil
}

func (r *codexVersionSyncSettingRepoStub) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.setErr != nil {
		return r.setErr
	}
	r.values[key] = value
	r.writes = append(r.writes, value)
	return nil
}

func (r *codexVersionSyncSettingRepoStub) syncedWrites() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.writes...)
}

type codexVersionSyncGitHubStub struct {
	GitHubReleaseClient // 嵌入接口，未实现的方法会 panic（不应被调用）

	releases []*GitHubRelease
	err      error
	calls    int
}

func (c *codexVersionSyncGitHubStub) FetchRecentReleases(_ context.Context, _ string, _ int) ([]*GitHubRelease, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.releases, nil
}

func newCodexVersionSyncService(
	repo SettingRepository,
	github GitHubReleaseClient,
) *OpenAICodexVersionSyncService {
	return NewOpenAICodexVersionSyncService(repo, &SettingService{}, github, openAICodexVersionSyncInterval)
}

// 同仓库同时发布预发布版与其他组件的 tag，必须只认 rust-v 前缀的稳定版，
// 否则会把无关组件的版本号（如 rusty-v8-v150.4.0）当成客户端版本同步出去。
func TestLatestCodexStableReleaseVersion(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "rusty-v8-v150.4.0"},
		{TagName: "rust-v0.147.0-alpha.4", Prerelease: true},
		{TagName: "rust-v0.146.0"},
		{TagName: "rust-v0.145.0"},
		{TagName: "rust-v0.999.0", Draft: true},
		{TagName: "not-a-tag"},
		nil,
	}

	require.Equal(t, "0.146.0", latestCodexStableReleaseVersion(releases))
	require.Empty(t, latestCodexStableReleaseVersion(nil))
	require.Empty(t, latestCodexStableReleaseVersion([]*GitHubRelease{{TagName: "rusty-v8-v150.4.0"}}))
	// 预发布 tag 即使漏标 Prerelease 也要被版本号后缀挡住。
	require.Empty(t, latestCodexStableReleaseVersion([]*GitHubRelease{{TagName: "rust-v0.147.0-alpha.4"}}))
}

func TestOpenAICodexVersionSyncWritesLatestStableVersion(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(nil)
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{
		{TagName: "rust-v0.145.0"},
		{TagName: "rust-v0.146.0"},
	}}

	newCodexVersionSyncService(repo, github).runOnce()

	require.Equal(t, []string{"0.146.0"}, repo.syncedWrites())
}

// 只向前推进：上游偶发返回旧数据或重新发布历史 tag 时不把已同步版本降级。
func TestOpenAICodexVersionSyncNeverMovesBackwards(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyOpenAICodexClientVersionSynced: "0.146.0",
	})
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.145.0"}}}

	newCodexVersionSyncService(repo, github).runOnce()

	require.Empty(t, repo.syncedWrites())
}

func TestOpenAICodexVersionSyncSkippedWhenDisabled(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyOpenAICodexVersionAutoSyncEnabled: "false",
	})
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.146.0"}}}

	newCodexVersionSyncService(repo, github).runOnce()

	require.Zero(t, github.calls, "关闭自动同步后不应请求上游")
	require.Empty(t, repo.syncedWrites())
}

// 面板开关缺失或为空一律视为开启，与设置默认值一致。
func TestOpenAICodexVersionSyncEnabledByDefault(t *testing.T) {
	for _, value := range []string{"", "true"} {
		repo := newCodexVersionSyncSettingRepoStub(map[string]string{
			SettingKeyOpenAICodexVersionAutoSyncEnabled: value,
		})
		github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.146.0"}}}

		newCodexVersionSyncService(repo, github).runOnce()

		require.Equal(t, []string{"0.146.0"}, repo.syncedWrites(), "开关值 %q", value)
	}
}

// 抓取失败保持既有值，不清空、不降级。
func TestOpenAICodexVersionSyncKeepsValueOnFetchError(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyOpenAICodexClientVersionSynced: "0.146.0",
	})
	github := &codexVersionSyncGitHubStub{err: errors.New("network down")}

	newCodexVersionSyncService(repo, github).runOnce()

	require.Empty(t, repo.syncedWrites())
	value, err := repo.GetValue(context.Background(), SettingKeyOpenAICodexClientVersionSynced)
	require.NoError(t, err)
	require.Equal(t, "0.146.0", value)
}

// 依赖缺失时 Start 必须直接返回，不能起一个空转的 goroutine。
func TestOpenAICodexVersionSyncStartRequiresDependencies(t *testing.T) {
	require.NotPanics(t, func() {
		svc := NewOpenAICodexVersionSyncService(nil, nil, nil, openAICodexVersionSyncInterval)
		svc.Start()
		svc.Stop()
	})
}

// --- mock: 版本号读取只用到 GetMultiple ---

type codexVersionSettingRepoStub struct {
	SettingRepository // 嵌入接口，未实现的方法会 panic（不应被调用）

	values map[string]string
	err    error
}

func (r *codexVersionSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = r.values[key]
	}
	return out, nil
}

// 规范 UA 解析会先读面板的完整 UA 键。
func (r *codexVersionSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.values[key], nil
}

// 版本号优先级：管理员面板覆写 → 自动同步值 → 内置常量。
// 管理员覆写必须压过同步值，否则「固定版本」的诉求会被 3 小时后的同步冲掉。
func TestGetOpenAICodexClientVersionPriority(t *testing.T) {
	tests := []struct {
		name     string
		override string
		synced   string
		want     string
	}{
		{name: "面板覆写优先", override: "0.150.0", synced: "0.146.0", want: "0.150.0"},
		{name: "覆写为空时用同步值", synced: "0.146.0", want: "0.146.0"},
		{name: "两者皆空时用内置常量", want: codexCLIVersion},
		{name: "非法覆写回退同步值", override: "latest", synced: "0.146.0", want: "0.146.0"},
		{name: "非法同步值回退内置常量", synced: "not-a-version", want: codexCLIVersion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
				SettingKeyOpenAICodexClientVersion:       tt.override,
				SettingKeyOpenAICodexClientVersionSynced: tt.synced,
			}}, nil)

			require.Equal(t, tt.want, svc.GetOpenAICodexClientVersion(context.Background()))
		})
	}
}

// 读取失败必须回退内置常量，不能返回空串把畸形身份拼给上游。
func TestGetOpenAICodexClientVersionFallsBackOnError(t *testing.T) {
	svc := NewSettingService(&codexVersionSettingRepoStub{err: errors.New("db down")}, nil)
	require.Equal(t, codexCLIVersion, svc.GetOpenAICodexClientVersion(context.Background()))
}

// 规范 UA：面板未填完整 UA 时按当前生效版本号拼出标准 CLI 形态。
func TestGetOpenAICodexCanonicalUserAgentBuildsFromVersion(t *testing.T) {
	svc := NewSettingService(&codexVersionSettingRepoStub{values: map[string]string{
		SettingKeyOpenAICodexClientVersionSynced: "0.200.1",
	}}, nil)

	require.Equal(t,
		"codex_cli_rs/0.200.1"+codexCLIUserAgentSuffix,
		svc.GetOpenAICodexCanonicalUserAgent(context.Background()),
	)
}

func (r *codexVersionSyncSettingRepoStub) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, r.getErr
	}
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value, UpdatedAt: r.updatedAt}, nil
}

// 启动同步防抖：同步值仍在一个周期内时跳过，避免频繁重启/滚动发布把启动同步
// 放大成对 GitHub 的连续请求。
func TestOpenAICodexVersionSyncInitialSkipsWhenRecentlySynced(t *testing.T) {
	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyOpenAICodexClientVersionSynced: "0.146.0",
	})
	repo.updatedAt = time.Now().Add(-time.Hour)
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.147.0"}}}

	newCodexVersionSyncService(repo, github).runInitial()

	require.Zero(t, github.calls, "同步值仍在周期内时不应请求上游")
	require.Empty(t, repo.syncedWrites())
}

func TestOpenAICodexVersionSyncInitialRunsWhenStaleOrMissing(t *testing.T) {
	t.Run("同步值已过期", func(t *testing.T) {
		repo := newCodexVersionSyncSettingRepoStub(map[string]string{
			SettingKeyOpenAICodexClientVersionSynced: "0.146.0",
		})
		repo.updatedAt = time.Now().Add(-7 * time.Hour)
		github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.147.0"}}}

		newCodexVersionSyncService(repo, github).runInitial()

		require.Equal(t, 1, github.calls)
		require.Equal(t, []string{"0.147.0"}, repo.syncedWrites())
	})

	// 首次部署尚无同步值：必须立刻同步，不能被防抖挡住。
	t.Run("尚无同步值", func(t *testing.T) {
		repo := newCodexVersionSyncSettingRepoStub(nil)
		repo.updatedAt = time.Now()
		github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.146.0"}}}

		newCodexVersionSyncService(repo, github).runInitial()

		require.Equal(t, 1, github.calls)
		require.Equal(t, []string{"0.146.0"}, repo.syncedWrites())
	})
}

// 版本比较必须按段取数字：字典序会把 0.99.0 判为大于 0.146.0，
// 从而让「取最大值」和「只向前推进」两处逻辑同时判错。
func TestCodexVersionComparisonIsNumericNotLexical(t *testing.T) {
	require.Greater(t, CompareVersions("0.146.0", "0.99.0"), 0)

	require.Equal(t, "0.146.0", latestCodexStableReleaseVersion([]*GitHubRelease{
		{TagName: "rust-v0.99.0"},
		{TagName: "rust-v0.146.0"},
	}))

	repo := newCodexVersionSyncSettingRepoStub(map[string]string{
		SettingKeyOpenAICodexClientVersionSynced: "0.146.0",
	})
	github := &codexVersionSyncGitHubStub{releases: []*GitHubRelease{{TagName: "rust-v0.99.0"}}}

	newCodexVersionSyncService(repo, github).runOnce()

	require.Empty(t, repo.syncedWrites(), "0.99.0 低于已同步的 0.146.0，不得写入")
}
