//go:build unit

package repository

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// countingQueryMatcher 与 captureProjectionQueryMatcher 行为一致（匹配任意查询），
// 额外记录实际执行的查询次数，用于断言缓存命中 / singleflight 场景下 DB 只被查询一次。
type countingQueryMatcher struct {
	mu    sync.Mutex
	count int
	sql   *string
}

func (m *countingQueryMatcher) Match(_, actual string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count++
	if m.sql != nil {
		*m.sql = actual
	}
	return nil
}

func (m *countingQueryMatcher) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

func newModelAvailabilityCandidateRepo(t *testing.T, counter *countingQueryMatcher) (*accountRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(counter))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	return newAccountRepositoryWithSQL(client, db, nil), mock
}

// modelAvailabilityCandidateRow 声明投影查询的固定列序（与实现 SELECT 列表一一对应）。
func modelAvailabilityCandidateRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "platform", "type", "concurrency", "priority",
		"model_mapping", "oauth_type", "compact_model_mapping", "openai_capabilities",
		"aws_region", "aws_force_global",
		"privacy_mode", "openai_passthrough", "mixed_scheduling",
		"openai_compact_mode", "openai_compact_supported",
		"openai_ws_force_http", "openai_oauth_ws_mode",
		"openai_apikey_responses_websockets_v2_mode",
		"openai_apikey_responses_websockets_v2_enabled",
		"responses_websockets_v2_enabled", "openai_ws_enabled",
	})
}

// addOAuthCandidateRow 追加一个典型的 OpenAI OAuth 候选行：显式 model_mapping、
// openai_capabilities、privacy_mode、WS OAuth 模式等子键齐全。
func addOAuthCandidateRow(rows *sqlmock.Rows) *sqlmock.Rows {
	return rows.AddRow(
		int64(7), "openai", "oauth", 3, 100,
		`{"gpt-4o":"gpt-4o","codex-mini-latest":"codex-mini-latest"}`, nil, nil,
		`["chat_completions"]`, nil, nil,
		`"training_off"`, nil, nil,
		`"auto"`, nil,
		nil, `"managed_session"`, nil,
		nil, nil, nil,
	)
}

// TestListModelAvailabilityCandidates_CacheHitSkipsSecondQuery 验证 TTL 内相同
// groupID 的第二次调用直接命中缓存，DB 查询次数必须为 1。
func TestListModelAvailabilityCandidates_CacheHitSkipsSecondQuery(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))

	groupID := int64(42)
	first, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, int64(7), first[0].ID)

	// 相同 groupID + 相同参数在 TTL 内再次调用，应命中缓存，不再查询 DB。
	second, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, 1, counter.Count(), "第二次调用应命中缓存，DB 查询次数必须为 1")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListModelAvailabilityCandidates_CacheExpiresAfterTTL 验证 TTL 到期后重新查询。
func TestListModelAvailabilityCandidates_CacheExpiresAfterTTL(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	// 注入可控时钟，模拟 30s TTL 到期。
	fakeNow := time.Unix(1_700_000_000, 0)
	repo.modelAvailabilityCache.now = func() time.Time { return fakeNow }

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))
	rows := modelAvailabilityCandidateRow()
	rows.AddRow(int64(9), "openai", "api_key", 2, 80, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mock.ExpectQuery("model availability candidates").WillReturnRows(rows)

	groupID := int64(42)
	first, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, first, 1)

	fakeNow = fakeNow.Add(modelAvailabilityCandidatesCacheTTL + time.Second)
	second, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, int64(9), second[0].ID, "TTL 到期后应重新查询并看到最新数据")
	require.Equal(t, 2, counter.Count())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListModelAvailabilityCandidates_ProjectionDecodesOnlySubset 验证投影解码
// 形状：凭据只包含判别所需子键，敏感凭据（access_token 等）不进内存。
func TestListModelAvailabilityCandidates_ProjectionDecodesOnlySubset(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))

	groupID := int64(42)
	accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	acc := accounts[0]
	require.Equal(t, int64(7), acc.ID)
	require.Equal(t, "openai", acc.Platform)
	require.Equal(t, "oauth", acc.Type)
	require.Equal(t, 3, acc.Concurrency)

	require.Equal(t, map[string]any{"gpt-4o": "gpt-4o", "codex-mini-latest": "codex-mini-latest"}, acc.Credentials["model_mapping"])
	require.Equal(t, []any{"chat_completions"}, acc.Credentials["openai_capabilities"])
	for _, sensitive := range []string{"access_token", "refresh_token", "id_token", "api_key", "session_key", "personal_access_token", "user_agent"} {
		_, ok := acc.Credentials[sensitive]
		require.False(t, ok, "投影结果不得包含敏感凭据字段 %s", sensitive)
	}

	require.Equal(t, "training_off", acc.Extra["privacy_mode"])
	for _, unrelated := range []string{"openai_device_id", "openai_session_id", "codex_usage_updated_at", "model_rate_limits"} {
		_, ok := acc.Extra[unrelated]
		require.False(t, ok, "投影结果不得包含无关 extra 字段 %s", unrelated)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListModelAvailabilityCandidates_ProjectedSQLSelectsSubKeysOnly 验证 SQL
// 投影：SELECT 列表只含判别所需列与 credentials/extra 的 JSONB 子键，
// 不选择裸的 credentials/extra 全量列。
func TestListModelAvailabilityCandidates_ProjectedSQLSelectsSubKeysOnly(t *testing.T) {
	// 分组路径
	{
		var captured string
		counter := &countingQueryMatcher{sql: &captured}
		repo, mock := newModelAvailabilityCandidateRepo(t, counter)
		mock.ExpectQuery("model availability candidates").
			WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))
		groupID := int64(42)
		_, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		normalized := normalizeSQLWhitespace(captured)
		assertModelAvailabilityProjectionSelect(t, normalized)
		require.Contains(t, normalized, "group_id")
	}

	// 未分组路径
	{
		var captured string
		counter := &countingQueryMatcher{sql: &captured}
		repo, mock := newModelAvailabilityCandidateRepo(t, counter)
		mock.ExpectQuery("model availability candidates").
			WillReturnRows(modelAvailabilityCandidateRow())
		_, err := repo.ListModelAvailabilityCandidates(context.Background(), nil, []string{service.PlatformOpenAI}, false)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		normalized := normalizeSQLWhitespace(captured)
		assertModelAvailabilityProjectionSelect(t, normalized)
		require.Contains(t, normalized, "NOT EXISTS")
	}

	// 未分组路径 + includeGrouped=true（简单模式全量池判别）：不排除已绑定分组的账号。
	{
		var captured string
		counter := &countingQueryMatcher{sql: &captured}
		repo, mock := newModelAvailabilityCandidateRepo(t, counter)
		mock.ExpectQuery("model availability candidates").
			WillReturnRows(modelAvailabilityCandidateRow())
		_, err := repo.ListModelAvailabilityCandidates(context.Background(), nil, []string{service.PlatformOpenAI}, true)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		normalized := normalizeSQLWhitespace(captured)
		assertModelAvailabilityProjectionSelect(t, normalized)
		require.NotContains(t, normalized, "NOT EXISTS", "includeGrouped=true 时不得排除已绑定分组的账号")
	}
}

func TestModelAvailabilityProjection_PassthroughUsesBooleanFallback(t *testing.T) {
	selectList := modelAvailabilityCandidateSelectList("a")
	// The SQL expression must mirror IsOpenAIPassthroughEnabled: malformed
	// values in the new key do not shadow a valid legacy OAuth boolean.
	require.Contains(t, selectList, "CASE WHEN jsonb_typeof(a.extra->'openai_passthrough') = 'boolean'")
	require.Contains(t, selectList, "WHEN jsonb_typeof(a.extra->'openai_oauth_passthrough') = 'boolean'")
}

// assertModelAvailabilityProjectionSelect 断言 SELECT 列表不含裸的 credentials/extra
// 列，且包含判别所需的 JSONB 子键投影。
func assertModelAvailabilityProjectionSelect(t *testing.T, normalized string) {
	t.Helper()
	selectClause, _, found := strings.Cut(normalized, " FROM ")
	require.True(t, found, "unexpected SQL: %s", normalized)
	for _, item := range strings.Split(selectClause, ",") {
		item = strings.TrimSpace(item)
		for _, forbidden := range []string{"credentials", "extra", "a.credentials", "a.extra"} {
			require.NotEqual(t, forbidden, item, "禁止全量选择 %s 列: %s", forbidden, normalized)
		}
	}
	require.Contains(t, selectClause, "credentials->'model_mapping'")
	require.Contains(t, selectClause, "credentials->'oauth_type'")
	require.Contains(t, selectClause, "credentials->'compact_model_mapping'")
	require.Contains(t, selectClause, "credentials->'openai_capabilities'")
	require.Contains(t, selectClause, "extra->'privacy_mode'")
	require.Contains(t, selectClause, "extra->'openai_passthrough'")
}

// TestListModelAvailabilityCandidates_ProjectedAccountsPreserveSupportSemantics
// 验证投影结果与原全量账号在 IsModelSupported 判别上语义一致：
// mapping 白名单与 passthrough fail-open 行为不变。
func TestListModelAvailabilityCandidates_ProjectedAccountsPreserveSupportSemantics(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	rows := modelAvailabilityCandidateRow()
	// OAuth：显式 model_mapping 决定支持集。
	rows.AddRow(int64(7), "openai", "oauth", 3, 100,
		`{"gpt-5.4-high":"gpt-5.4-high"}`, nil, nil, nil, nil, nil,
		`"training_off"`, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// APIKey：openai_passthrough=true → 任何模型都支持。
	rows.AddRow(int64(9), "openai", "api_key", 2, 80,
		nil, nil, nil, nil, nil, nil,
		nil, true, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	mock.ExpectQuery("model availability candidates").WillReturnRows(rows)

	groupID := int64(42)
	accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, accounts, 2)

	require.True(t, accounts[0].IsModelSupported("gpt-5.4-high"), "mapping 内模型应受支持")
	require.False(t, accounts[0].IsModelSupported("deepseek-v4"), "mapping 外模型应不受支持")
	require.True(t, accounts[1].IsModelSupported("anything-else"), "passthrough 账号应支持任意模型")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListModelAvailabilityCandidates_ConcurrentMissQueriesOnce 验证 TTL 到期后的
// 并发 miss 被 singleflight 合并为一次 DB 查询。
func TestListModelAvailabilityCandidates_ConcurrentMissQueriesOnce(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))

	groupID := int64(42)
	var wg sync.WaitGroup
	results := make([][]service.Account, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
		}(i)
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		require.NoError(t, errs[i])
		require.Len(t, results[i], 1)
	}
	require.Equal(t, 1, counter.Count(), "并发缓存命中不应增加 DB 查询")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListModelAvailabilityCandidates_ConcurrentSupportChecksRaceFree 验证缓存
// 返回的账号由多调用方直接共享（无拷贝）：并发对同一缓存条目做
// IsModelSupported 不得产生数据竞争——并发安全性来自 GetModelMapping 的
// atomic.Value 惰性记忆化（非拷贝）。两轮并发：第一轮共享 singleflight
// leader 的结果，第二轮共享缓存 GET 的条目。
func TestListModelAvailabilityCandidates_ConcurrentSupportChecksRaceFree(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))

	groupID := int64(42)
	// Warm the entry first.  The singleflight miss behavior is covered by the
	// dedicated ConcurrentMissQueriesOnce test above; this test focuses on the
	// race-free shared cached account/mapping memo under concurrent readers.
	warm, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, warm, 1)
	require.True(t, warm[0].IsModelSupported("gpt-4o"))
	run := func() {
		start := make(chan struct{})
		errs := make([]error, 16)
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
				errs[i] = err
				if err == nil && len(accounts) > 0 {
					_ = accounts[0].IsModelSupported("gpt-4o")
					_ = accounts[0].IsModelSupported("deepseek-v4")
				}
			}(i)
		}
		close(start)
		wg.Wait()
		for i := range errs {
			require.NoError(t, errs[i])
		}
	}

	// 第一轮：并发缓存命中，共享同一账号切片与 model_mapping memo。
	run()
	require.Equal(t, 1, counter.Count(), "并发 miss 应被 singleflight 合并为一次查询")
	// 第二轮：TTL 内全部命中缓存，走缓存 GET 路径。
	run()
	require.Equal(t, 1, counter.Count(), "第二轮应全部命中缓存，不再查询 DB")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestListModelAvailabilityCandidates_CachedPathZeroAllocations 验证缓存命中
// 路径零分配：缓存 GET 直接返回共享切片（无每请求克隆），并发安全的
// model_mapping memo 命中时不分配。旧实现每账号每次判别都会克隆切片并计算
// 签名，分配随账号数线性增长（pprof alloc_space 定位的 5.17GB 热点）。
func TestListModelAvailabilityCandidates_CachedPathZeroAllocations(t *testing.T) {
	counter := &countingQueryMatcher{}
	repo, mock := newModelAvailabilityCandidateRepo(t, counter)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))

	groupID := int64(42)
	// 预热：填充缓存条目并触发 memo 首次计算，使待测闭包全部走命中路径。
	accounts, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.True(t, accounts[0].IsModelSupported("codex-mini-latest"))

	// 平台切片提升到闭包外：它在生产调用方按请求构建（常量成本），
	// 与账号数无关；context 也由真实请求在调用前创建。待测闭包只衡量
	// 缓存命中 + 每账号判别本身。
	platforms := []string{service.PlatformOpenAI}
	ctx := context.Background()
	allocs := testing.AllocsPerRun(200, func() {
		got, err := repo.ListModelAvailabilityCandidates(ctx, &groupID, platforms, false)
		if err != nil {
			t.Error(err)
			return
		}
		_ = got[0].IsModelSupported("codex-mini-latest")
	})
	require.Zero(t, allocs, "缓存命中 + memo 命中路径必须零分配，实际 %.2f allocs/run", allocs)
	require.NoError(t, mock.ExpectationsWereMet())
}

// gatedSQLExecutor 捕获 QueryContext 收到的 ctx 并阻塞到放行后转发给底层 DB：
// 用于验证 singleflight leader 的查询上下文是否脱离首个调用方的取消。
type gatedSQLExecutor struct {
	db       *sql.DB
	started  chan struct{} // 首次捕获 ctx 后关闭
	release  chan struct{} // 关闭后放行查询
	captured context.Context
	mu       sync.Mutex
}

func (g *gatedSQLExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	g.mu.Lock()
	if g.captured == nil {
		g.captured = ctx
		close(g.started)
	}
	g.mu.Unlock()
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.db.QueryContext(ctx, query, args...)
}

func (g *gatedSQLExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return g.db.ExecContext(ctx, query, args...)
}

// TestListModelAvailabilityCandidates_LeaderQueryDetachedFromCallerCancel 验证
// singleflight leader 的查询上下文脱离首个调用方的取消：首个请求及时返回
// context canceled，但查询继续服务其他等待者；查询仍带独立执行上限。
func TestListModelAvailabilityCandidates_LeaderQueryDetachedFromCallerCancel(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(&countingQueryMatcher{}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	gate := &gatedSQLExecutor{db: db, started: make(chan struct{}), release: make(chan struct{})}
	repo := newAccountRepositoryWithSQL(client, gate, nil)

	mock.ExpectQuery("model availability candidates").
		WillReturnRows(addOAuthCandidateRow(modelAvailabilityCandidateRow()))

	groupID := int64(42)
	callerCtx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := repo.ListModelAvailabilityCandidates(callerCtx, &groupID, []string{service.PlatformOpenAI}, false)
		leaderDone <- err
	}()

	// 等 leader 进入查询后加入第二个 waiter，再取消首个调用方。
	<-gate.started
	waiterDone := make(chan error, 1)
	go func() {
		_, err := repo.ListModelAvailabilityCandidates(context.Background(), &groupID, []string{service.PlatformOpenAI}, false)
		waiterDone <- err
	}()
	cancel()

	deadline, ok := gate.captured.Deadline()
	require.True(t, ok, "leader 查询上下文必须带独立执行上限")
	_ = deadline
	require.NoError(t, gate.captured.Err(), "调用方取消不得传播到共享 leader")
	select {
	case err := <-leaderDone:
		require.ErrorIs(t, err, context.Canceled, "首个 waiter 应按自身 context 及时取消")
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not return promptly")
	}

	// 放行查询：第二个 waiter 应复用未被取消的共享 leader 并成功返回。
	close(gate.release)
	select {
	case err := <-waiterDone:
		require.NoError(t, err, "取消首个调用方不得中断其他 waiter")
	case <-time.After(5 * time.Second):
		t.Fatal("shared query did not return after release")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
