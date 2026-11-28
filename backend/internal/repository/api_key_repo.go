package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

type apiKeyRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

func NewAPIKeyRepository(client *dbent.Client, sqlDB *sql.DB) service.APIKeyRepository {
	return newAPIKeyRepositoryWithSQL(client, sqlDB)
}

func newAPIKeyRepositoryWithSQL(client *dbent.Client, sqlq sqlExecutor) *apiKeyRepository {
	return &apiKeyRepository{client: client, sql: sqlq}
}

func (r *apiKeyRepository) activeQuery() *dbent.APIKeyQuery {
	// 默认过滤已软删除记录，避免删除后仍被查询到。
	return r.client.APIKey.Query().Where(apikey.DeletedAtIsNil())
}

func (r *apiKeyRepository) Create(ctx context.Context, key *service.APIKey) error {
	// 凭据本体只落哈希：即使调用方内存里还有明文（创建回执要用），
	// 写库时也绝不写 api_keys.key。key_hash / key_prefix 由内存对象补齐。
	key.EnsureKeyHash()
	keyHash, keyPrefix := key.KeyHash, key.KeyPrefix

	create := func(exec *dbent.Client) error {
		builder := exec.APIKey.Create().
			SetUserID(key.UserID).
			SetName(key.Name).
			SetConcurrencyLimit(key.ConcurrencyLimit).
			SetStatus(key.Status).
			SetNillableGroupID(key.GroupID).
			SetNillableLastUsedAt(key.LastUsedAt).
			SetQuota(key.Quota).
			SetQuotaUsed(key.QuotaUsed).
			SetNillableExpiresAt(key.ExpiresAt).
			SetRateLimit5h(key.RateLimit5h).
			SetRateLimit1d(key.RateLimit1d).
			SetRateLimit7d(key.RateLimit7d).
			SetAnthropicCacheTTLMode(service.NormalizeAnthropicCacheTTLMode(key.AnthropicCacheTTLMode)).
			SetDeepseekToolInjectionEnabled(key.IsDeepSeekToolInjectionEnabled()).
			SetAutoBlockDatacenterIps(key.AutoBlockDatacenterIPs)

		if keyHash != "" {
			builder.SetKeyHash(keyHash).SetKeyPrefix(keyPrefix)
		}
		if len(key.IPWhitelist) > 0 {
			builder.SetIPWhitelist(key.IPWhitelist)
		}
		if len(key.IPBlacklist) > 0 {
			builder.SetIPBlacklist(key.IPBlacklist)
		}

		created, err := builder.Save(ctx)
		if err != nil {
			return err
		}
		key.ID = created.ID
		key.LastUsedAt = created.LastUsedAt
		key.CreatedAt = created.CreatedAt
		key.UpdatedAt = created.UpdatedAt
		return nil
	}

	err := r.withTx(ctx, func(exec *dbent.Client) error {
		if err := create(exec); err != nil {
			return err
		}
		// 独立哈希表与 api_keys.key_hash 同事务双写，避免只写成功一处。
		return r.upsertAPIKeyHash(ctx, exec, key.ID, keyHash, keyPrefix)
	})
	return translatePersistenceError(err, nil, service.ErrAPIKeyExists)
}

func (r *apiKeyRepository) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	m, err := r.activeQuery().
		Where(apikey.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return r.apiKeyEntityToServiceWithRoutes(ctx, m)
}

// GetKeyHashAndOwnerID 根据 API Key ID 获取其 key_hash 与所有者（用户）ID。
// 相比 GetByID，此方法性能更优，因为：
//   - 使用 Select() 只查询必要字段，减少数据传输量
//   - 不加载完整的 API Key 实体及其关联数据（User、Group 等）
//   - 适用于删除等只需「凭据标识」与用户 ID 的场景
//
// 返回的是 sha256 摘要而不是明文：明文从迁移 327 之后不再落库，而认证缓存
// 键本身就等于 sha256(明文)，所以拿哈希就能失效对应缓存。
func (r *apiKeyRepository) GetKeyHashAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	m, err := r.activeQuery().
		Where(apikey.IDEQ(id)).
		Select(apikey.FieldKeyHash, apikey.FieldKey, apikey.FieldUserID).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", 0, service.ErrAPIKeyNotFound
		}
		return "", 0, err
	}
	// 过渡期：尚未回填 key_hash 的历史行只能回退明文，否则删除后旧缓存仍可用。
	credential := m.KeyHash
	if credential == "" {
		credential = m.Key
	}
	return credential, m.UserID, nil
}

func (r *apiKeyRepository) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	m, err := r.findByCredential(ctx, key, func(q *dbent.APIKeyQuery) *dbent.APIKeyQuery {
		return q.
			WithUser(func(q *dbent.UserQuery) {
				q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
					gq.Select(group.FieldID)
				})
			}).
			WithGroup()
	})
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return r.apiKeyEntityToServiceWithRoutes(ctx, m)
}

// findByCredential 按 key_hash 等值查询，找不到时回退到历史明文列。
//
// 回退是过渡期行为，只为「迁移 327 已上线但生产回填还没跑完」的历史行：
// 这些行 key 里还有明文、key_hash 还是 NULL，只能按明文命中。新行与已回填
// 行 key 为 NULL，明文查询永远命中不到，所以回退不会泄露或放大权限。
// 阶段 B 清理完成、确认 count(明文且无哈希)=0 之后，可删掉回退分支。
func (r *apiKeyRepository) findByCredential(ctx context.Context, key string, decorate func(*dbent.APIKeyQuery) *dbent.APIKeyQuery) (*dbent.APIKey, error) {
	if key == "" {
		return nil, service.ErrAPIKeyNotFound
	}
	hash := service.HashAPIKey(key)
	m, err := decorate(r.activeQuery().Where(apikey.KeyHashEQ(hash))).Only(ctx)
	if err == nil {
		return m, nil
	}
	if !dbent.IsNotFound(err) {
		return nil, err
	}
	return decorate(r.activeQuery().Where(apikey.KeyEQ(key))).Only(ctx)
}

func (r *apiKeyRepository) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	m, err := r.findByCredential(ctx, key, func(q *dbent.APIKeyQuery) *dbent.APIKeyQuery {
		return q.Select(
			apikey.FieldID,
			apikey.FieldUserID,
			apikey.FieldGroupID,
			apikey.FieldName,
			apikey.FieldKeyHash,
			apikey.FieldKeyPrefix,
			apikey.FieldStatus,
			apikey.FieldIPWhitelist,
			apikey.FieldIPBlacklist,
			apikey.FieldQuota,
			apikey.FieldQuotaUsed,
			apikey.FieldExpiresAt,
			apikey.FieldRateLimit5h,
			apikey.FieldRateLimit1d,
			apikey.FieldRateLimit7d,
			// 密钥级 Anthropic cache_control TTL 覆盖。少这一列，鉴权快照里
			// AnthropicCacheTTLMode 恒为空 -> 归一化成 inherit -> 网关读不到
			// 管理员设的「强制 1h」，整条注入链路静默失效。
			apikey.FieldAnthropicCacheTTLMode,
			apikey.FieldDeepseekToolInjectionEnabled,
			apikey.FieldAutoBlockDatacenterIps,
			apikey.FieldConcurrencyLimit,
		).
			WithUser(func(q *dbent.UserQuery) {
				q.Select(
					user.FieldID,
					user.FieldEmail,
					user.FieldUsername,
					user.FieldStatus,
					user.FieldRole,
					user.FieldBalance,
					user.FieldConcurrency,
					user.FieldBalanceNotifyEnabled,
					user.FieldRestrictPublicGroups,
					user.FieldBalanceNotifyThresholdType,
					user.FieldBalanceNotifyThreshold,
					user.FieldBalanceNotifyExtraEmails,
					user.FieldBalanceNotifyPrimaryEmailDisabled,
					user.FieldTotalRecharged,
					user.FieldSignupSource,
					user.FieldLastLoginAt,
					user.FieldLastActiveAt,
					user.FieldRpmLimit,
				)
				q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
					gq.Select(group.FieldID)
				})
			}).
			WithGroup(func(q *dbent.GroupQuery) {
				q.Select(
					group.FieldID,
					group.FieldName,
					group.FieldPlatform,
					group.FieldIsExclusive,
					group.FieldStatus,
					group.FieldSubscriptionType,
					group.FieldRateMultiplier,
					group.FieldDailyLimitUsd,
					group.FieldWeeklyLimitUsd,
					group.FieldMonthlyLimitUsd,
					group.FieldAllowImageGeneration,
					group.FieldAllowBatchImageGeneration,
					group.FieldImageRateIndependent,
					group.FieldImageRateMultiplier,
					group.FieldImagePrice1k,
					group.FieldImagePrice2k,
					group.FieldImagePrice4k,
					group.FieldVideoRateIndependent,
					group.FieldVideoRateMultiplier,
					group.FieldVideoPrice480p,
					group.FieldVideoPrice720p,
					group.FieldVideoPrice1080p,
					group.FieldVideoModelPrices,
					group.FieldWebSearchPricePerCall,
					group.FieldToolRateIndependent,
					group.FieldToolRateMultiplier,
					group.FieldSearchPricePer1k,
					group.FieldFetchPricePer1k,
					group.FieldOcrPricePer1kPages,
					// 搜索专用凭据随分组一起取回：执行搜索要用，
					// 但只有配了搜索的分组才会走这条路径。
					group.FieldSearchProviderBaseURL,
					group.FieldSearchProviderAPIKey,
					group.FieldAudioRealtimePricePerMin,
					group.FieldAudioTtsPricePerMillionChars,
					group.FieldAudioSttPricePerHour,
					group.FieldLongContextPricingEnabled,
					group.FieldModelPricing,
					group.FieldClaudeCodeOnly,
					group.FieldFallbackGroupID,
					group.FieldFallbackGroupIDOnInvalidRequest,
					group.FieldModelRoutingEnabled,
					group.FieldModelRouting,
					group.FieldMcpXMLInject,
					group.FieldSupportedModelScopes,
					group.FieldAllowMessagesDispatch,
					group.FieldCrossProtocolConversionEnabled,
					group.FieldReturnRequestedModel,
					group.FieldSaveResponseMsgID,
					group.FieldAllowLive,
					group.FieldForceOpenaiFast,
					group.FieldFreeOpenaiFast,
					group.FieldOpenaiFastMultiplier,
					group.FieldOpenaiUltrafastMultiplier,
					group.FieldDefaultMappedModel,
					group.FieldMessagesDispatchModelConfig,
					group.FieldModelAllowlist,
					group.FieldCodexModelsManifestConfig,
					group.FieldRpmLimit,
					group.FieldMaxReasoningEffort,
					group.FieldMaxReasoningEffortOverLimit,
					group.FieldReasoningEffortMappings,
					group.FieldPeakRateEnabled,
					group.FieldPeakStart,
					group.FieldPeakEnd,
					group.FieldPeakRateMultiplier,
					// 分组利润控制：认证快照是调度门 enable 判定的直接来源，
					// 漏选会让门静默失效；新增快照分组字段时必须同步本投影，
					// 集成测试对账兜底。
					group.FieldProfitControlEnabled,
					group.FieldProfitMinMargin,
					group.FieldProfitSafetyBuffer,
				)
			})
	})
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return r.apiKeyEntityToServiceWithRoutes(ctx, m)
}

func (r *apiKeyRepository) Update(ctx context.Context, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	// 空掩码代表调用方不改任何列，直接返回，避免产生一次无意义的整行写。
	if fields.IsEmpty() {
		return nil
	}

	if fields.Key {
		// 轮换：新哈希、新前缀、清空明文列、更新 api_key_hashes 必须同事务，
		// 否则旧明文还会被过渡期回退查询命中（旧 key 未失效）。
		// 这里强制按 key.Key 重算而不是仅补空值：调用方可能只改了明文却带着
		// 旧哈希（例如直接调用仓库的脚本/测试），补空逻辑会把旧哈希写回去。
		key.KeyHash = service.HashAPIKey(key.Key)
		key.KeyPrefix = service.APIKeyPrefixFromPlaintext(key.Key)
		return r.withTx(ctx, func(exec *dbent.Client) error {
			if err := r.updateFields(ctx, exec, key, fields); err != nil {
				return err
			}
			if err := r.upsertAPIKeyHash(ctx, exec, key.ID, key.KeyHash, key.KeyPrefix); err != nil {
				return err
			}
			// 轮换后旧明文已经失效，浏览器端封存的旧密文也必须同时清掉；
			// 新密文由前端拿到新明文后重新封装（服务端不生成任何东西）。
			return deleteAPIKeySecretRow(ctx, exec, key.ID)
		})
	}

	return r.updateFields(ctx, clientFromContext(ctx, r.client), key, fields)
}

// updateFields 只写 fields 中显式声明的列，其余列保持库里当前值。
func (r *apiKeyRepository) updateFields(ctx context.Context, client *dbent.Client, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	// 使用原子操作：将软删除检查与更新合并到同一语句，避免竞态条件。
	// 之前的实现先检查 Exist 再 UpdateOneID，若在两步之间发生软删除，
	// 则会更新已删除的记录。
	// 这里选择 Update().Where()，确保只有未软删除记录能被更新。
	// 同时显式设置 updated_at，避免二次查询带来的并发可见性问题。
	now := time.Now()
	if fields.Key {
		// PostgreSQL stores microseconds; the rotation receipt must match the later list.
		now = now.Truncate(time.Microsecond)
	}
	builder := client.APIKey.Update().
		Where(apikey.IDEQ(key.ID), apikey.DeletedAtIsNil()).
		SetUpdatedAt(now)
	if fields.Name {
		builder.SetName(key.Name)
	}
	if fields.Status {
		builder.SetStatus(key.Status)
	}
	if fields.Key {
		// 轮换：写新哈希/前缀、清空明文列，并记录轮换时间（key_rotated_at）。
		builder.SetKeyHash(key.KeyHash).
			SetKeyPrefix(key.KeyPrefix).
			ClearKey().
			SetKeyRotatedAt(now)
	}
	if fields.Quota {
		builder.SetQuota(key.Quota)
	}
	if fields.QuotaUsed {
		builder.SetQuotaUsed(key.QuotaUsed)
	}
	if fields.RateLimits {
		builder.
			SetRateLimit5h(key.RateLimit5h).
			SetRateLimit1d(key.RateLimit1d).
			SetRateLimit7d(key.RateLimit7d)
	}
	if fields.ConcurrencyLimit {
		builder.SetConcurrencyLimit(key.ConcurrencyLimit)
	}
	if fields.AnthropicCacheTTLMode {
		builder.SetAnthropicCacheTTLMode(service.NormalizeAnthropicCacheTTLMode(key.AnthropicCacheTTLMode))
	}
	if fields.DeepSeekToolInjectionEnabled {
		builder.SetDeepseekToolInjectionEnabled(key.IsDeepSeekToolInjectionEnabled())
	}
	if fields.AutoBlockDatacenterIPs {
		builder.SetAutoBlockDatacenterIps(key.AutoBlockDatacenterIPs)
	}
	if fields.RateLimitUsage {
		builder.
			SetUsage5h(key.Usage5h).
			SetUsage1d(key.Usage1d).
			SetUsage7d(key.Usage7d)

		// Rate limit window start times
		if key.Window5hStart != nil {
			builder.SetWindow5hStart(*key.Window5hStart)
		} else {
			builder.ClearWindow5hStart()
		}
		if key.Window1dStart != nil {
			builder.SetWindow1dStart(*key.Window1dStart)
		} else {
			builder.ClearWindow1dStart()
		}
		if key.Window7dStart != nil {
			builder.SetWindow7dStart(*key.Window7dStart)
		} else {
			builder.ClearWindow7dStart()
		}
	}
	if fields.GroupID {
		if key.GroupID != nil {
			builder.SetGroupID(*key.GroupID)
		} else {
			builder.ClearGroupID()
		}
	}

	// Expiration time
	if fields.ExpiresAt {
		if key.ExpiresAt != nil {
			builder.SetExpiresAt(*key.ExpiresAt)
		} else {
			builder.ClearExpiresAt()
		}
	}

	// IP 限制字段
	if fields.IPRules {
		if len(key.IPWhitelist) > 0 {
			builder.SetIPWhitelist(key.IPWhitelist)
		} else {
			builder.ClearIPWhitelist()
		}
		if len(key.IPBlacklist) > 0 {
			builder.SetIPBlacklist(key.IPBlacklist)
		} else {
			builder.ClearIPBlacklist()
		}
	}

	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// 更新影响行数为 0，说明记录不存在或已被软删除。
		return service.ErrAPIKeyNotFound
	}

	// 使用同一时间戳回填，避免并发删除导致二次查询失败。
	key.UpdatedAt = now
	if fields.Key {
		key.KeyRotatedAt = &now
	}
	return nil
}

func (r *apiKeyRepository) Delete(ctx context.Context, id int64) error {
	// 显式软删除：避免依赖 Hook 行为，确保 deleted_at 一定被设置。
	// 同时清掉 key_hash 并删掉 api_key_hashes 行：唯一索引只为「活着的 key」
	// 保留，删除后同一个凭据（自定义 key）可以重新创建。
	client := clientFromContext(ctx, r.client)
	affected, err := client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		ClearKey().
		ClearKeyHash().
		SetDeletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrAPIKeyNotFound
		}
		return err
	}
	if affected == 0 {
		exists, err := client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		return service.ErrAPIKeyNotFound
	}
	if err := r.deleteAPIKeyHash(ctx, client, id); err != nil {
		return err
	}
	return deleteAPIKeySecretRow(ctx, client, id)
}

// DeleteWithAudit keeps the legacy method name for rolling-upgrade compatibility.
// It atomically clears credential material and soft-deletes the key.
func (r *apiKeyRepository) DeleteWithAudit(ctx context.Context, id int64) error {
	// 明文与哈希同时清空：软删后的行不再保留任何凭据材料，
	// api_key_hashes 的同事务删除负责释放新表唯一约束。
	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return r.deleteWithTombstone(ctx, existingTx.Client(), id)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}
	exec := r.client
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		exec = tx.Client()
	}

	if err := r.deleteWithTombstone(ctx, exec, id); err != nil {
		return err
	}

	if tx != nil {
		return tx.Commit()
	}
	return nil
}

func (r *apiKeyRepository) deleteWithTombstone(ctx context.Context, exec *dbent.Client, id int64) error {
	// 时间戳由应用侧传入而不是 NOW()：这条语句同时要能在 SQLite（单测）上跑。
	now := time.Now()
	res, err := exec.ExecContext(ctx, `
		UPDATE api_keys
		SET key = NULL, key_hash = NULL, deleted_at = $1, updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL`, now, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// 并发/重复删除:记录已存在(已软删)则幂等返回 nil(defer 回滚空事务),否则 NotFound。
		exists, existErr := r.client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if existErr != nil {
			return existErr
		}
		if exists {
			return nil
		}
		return service.ErrAPIKeyNotFound
	}
	if err := r.deleteAPIKeyHash(ctx, exec, id); err != nil {
		return err
	}
	return deleteAPIKeySecretRow(ctx, exec, id)
}

func (r *apiKeyRepository) apiKeyListByUserIDQuery(userID int64, filters service.APIKeyListFilters) *dbent.APIKeyQuery {
	q := r.activeQuery().Where(apikey.UserIDEQ(userID))

	if filters.Search != "" {
		// 明文列在阶段 B 清理后为 NULL，按前缀搜索必须落在 key_prefix 上，
		// 否则列表搜索会在新版上静默失效。
		q = q.Where(apikey.Or(
			apikey.NameContainsFold(filters.Search),
			apikey.KeyContainsFold(filters.Search),
			apikey.KeyPrefixContainsFold(filters.Search),
		))
	}
	if filters.Status != "" {
		q = q.Where(apikey.StatusEQ(filters.Status))
	}
	if filters.GroupID != nil {
		if *filters.GroupID == 0 {
			q = q.Where(apikey.GroupIDIsNil())
		} else {
			q = q.Where(apikey.GroupIDEQ(*filters.GroupID))
		}
	}

	return q
}

func (r *apiKeyRepository) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.apiKeyListByUserIDQuery(userID, filters)

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := q.
		WithGroup().
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	if err := r.attachGroupRoutes(ctx, outKeys); err != nil {
		return nil, nil, err
	}
	if err := r.attachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, nil, err
	}

	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

func (r *apiKeyRepository) ListAllByUserID(ctx context.Context, userID int64, filters service.APIKeyListFilters) ([]service.APIKey, error) {
	keys, err := r.apiKeyListByUserIDQuery(userID, filters).
		WithGroup().
		Order(dbent.Asc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	if err := r.attachGroupRoutes(ctx, outKeys); err != nil {
		return nil, err
	}
	if err := r.attachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, err
	}
	return outKeys, nil
}

func (r *apiKeyRepository) attachLastUsedIPs(ctx context.Context, keys []service.APIKey) error {
	if len(keys) == 0 || r.sql == nil {
		return nil
	}

	apiKeyIDs := make([]int64, 0, len(keys))
	for i := range keys {
		apiKeyIDs = append(apiKeyIDs, keys[i].ID)
	}

	lastUsedIPs, err := r.latestUsageLogIPs(ctx, apiKeyIDs)
	if err != nil {
		return err
	}
	for i := range keys {
		if ip, ok := lastUsedIPs[keys[i].ID]; ok {
			keys[i].LastUsedIP = &ip
		}
	}
	return nil
}

func (r *apiKeyRepository) latestUsageLogIPs(ctx context.Context, apiKeyIDs []int64) (result map[int64]string, err error) {
	if len(apiKeyIDs) == 0 || r.sql == nil {
		return map[int64]string{}, nil
	}

	query, args := latestUsageLogIPsQuery(apiKeyIDs, r.client.Driver().Dialect())
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	out := make(map[int64]string, len(apiKeyIDs))
	for rows.Next() {
		var apiKeyID int64
		var ipAddress string
		if err := rows.Scan(&apiKeyID, &ipAddress); err != nil {
			return nil, err
		}
		out[apiKeyID] = ipAddress
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func latestUsageLogIPsQuery(apiKeyIDs []int64, dialectName string) (string, []any) {
	if dialectName == dialect.Postgres {
		// Keep each key lookup bounded to one ordered index probe instead of ranking its full history.
		return `
		SELECT requested.api_key_id, latest.ip_address
		FROM unnest($1::bigint[]) AS requested(api_key_id)
		CROSS JOIN LATERAL (
			SELECT ul.ip_address
			FROM usage_logs AS ul
			WHERE ul.api_key_id = requested.api_key_id
				AND ul.ip_address IS NOT NULL
				AND ul.ip_address <> ''
			ORDER BY ul.created_at DESC, ul.id DESC
			LIMIT 1
		) AS latest`, []any{pq.Array(apiKeyIDs)}
	}

	placeholders := make([]string, len(apiKeyIDs))
	args := make([]any, len(apiKeyIDs))
	for i, id := range apiKeyIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	return fmt.Sprintf(`
		SELECT api_key_id, ip_address
		FROM (
			SELECT api_key_id, ip_address,
				ROW_NUMBER() OVER (PARTITION BY api_key_id ORDER BY created_at DESC, id DESC) AS rn
			FROM usage_logs
			WHERE api_key_id IN (%s)
				AND ip_address IS NOT NULL
				AND ip_address <> ''
		) ranked
		WHERE rn = 1`, strings.Join(placeholders, ", ")), args
}

func (r *apiKeyRepository) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	ids, err := r.client.APIKey.Query().
		Where(apikey.UserIDEQ(userID), apikey.IDIn(apiKeyIDs...), apikey.DeletedAtIsNil()).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *apiKeyRepository) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	count, err := r.activeQuery().Where(apikey.UserIDEQ(userID)).Count(ctx)
	return int64(count), err
}

// ExistsByKey 判断凭据是否已被占用。过渡期必须同时看两处：
//   - key_hash 命中：本版本之后创建的所有 key；
//   - key 明文命中：尚未回填 key_hash 的历史行（回填完就永远不命中）。
//
// 少看任何一处都会让「自定义 key 撞已有 key」在校验阶段漏过去，
// 最终撞在唯一索引上变成 500 而不是可读的「已存在」。
func (r *apiKeyRepository) ExistsByKey(ctx context.Context, key string) (bool, error) {
	if key == "" {
		return false, nil
	}
	count, err := r.activeQuery().
		Where(apikey.Or(
			apikey.KeyHashEQ(service.HashAPIKey(key)),
			apikey.KeyEQ(key),
		)).
		Count(ctx)
	return count > 0, err
}

func (r *apiKeyRepository) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	q := r.activeQuery().Where(apikey.GroupIDEQ(groupID))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := q.
		WithUser().
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range apiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}

	return outKeys, paginationResultFromTotal(int64(total), params), nil
}

func apiKeyListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	var field string
	switch sortBy {
	case "name":
		field = apikey.FieldName
	case "status":
		field = apikey.FieldStatus
	case "expires_at":
		field = apikey.FieldExpiresAt
	case "last_used_at":
		field = apikey.FieldLastUsedAt
	case "created_at":
		field = apikey.FieldCreatedAt
	case "id":
		field = apikey.FieldID
	default:
		field = apikey.FieldID
	}

	if sortOrder == pagination.SortOrderAsc {
		orders := []func(*entsql.Selector){dbent.Asc(field)}
		if field != apikey.FieldID {
			orders = append(orders, dbent.Asc(apikey.FieldID))
		}
		return orders
	}
	orders := []func(*entsql.Selector){dbent.Desc(field)}
	if field != apikey.FieldID {
		orders = append(orders, dbent.Desc(apikey.FieldID))
	}
	return orders
}

// SearchAPIKeys searches API keys by user ID and/or keyword (name)
func (r *apiKeyRepository) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]service.APIKey, error) {
	q := r.activeQuery()
	if userID > 0 {
		q = q.Where(apikey.UserIDEQ(userID))
	}

	if keyword != "" {
		q = q.Where(apikey.NameContainsFold(keyword))
	}

	keys, err := q.Limit(limit).Order(dbent.Desc(apikey.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]service.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *apiKeyEntityToService(keys[i]))
	}
	return outKeys, nil
}

// ClearGroupIDByGroupID 将指定分组的所有 API Key 的 group_id 设为 nil
func (r *apiKeyRepository) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	if r.sql != nil {
		if _, err := r.sql.ExecContext(ctx, "DELETE FROM api_key_group_routes WHERE group_id = $1", groupID); err != nil {
			return 0, err
		}
	}
	n, err := r.client.APIKey.Update().
		Where(apikey.GroupIDEQ(groupID), apikey.DeletedAtIsNil()).
		ClearGroupID().
		Save(ctx)
	return int64(n), err
}

// UpdateGroupIDByUserAndGroup 将用户下绑定 oldGroupID 的所有 Key 迁移到 newGroupID
// ListRecentIPAddresses returns the most recent distinct client IPs observed
// for one API key. It powers the operator's datacenter-IP blacklist action.
func (r *apiKeyRepository) ListRecentIPAddresses(ctx context.Context, apiKeyID int64, limit int) ([]string, error) {
	if r.sql == nil {
		return nil, errors.New("api key repository SQL executor not configured")
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT ip_address
		FROM usage_logs
		WHERE api_key_id = $1 AND COALESCE(ip_address, '') <> ''
		GROUP BY ip_address
		ORDER BY MAX(created_at) DESC
		LIMIT $2`, apiKeyID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

// ListAutoBlockDatacenterIPsKeys returns the keys that opted into the periodic
// datacenter/proxy IP blacklist job. Ordering by updated_at keeps the scan
// bounded while rotating which keys are visited when more than limit are on:
// every processed key bumps updated_at, so it moves to the back of the queue.
func (r *apiKeyRepository) ListAutoBlockDatacenterIPsKeys(ctx context.Context, limit int) ([]service.APIKey, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.activeQuery().
		Where(apikey.AutoBlockDatacenterIpsEQ(true)).
		Order(dbent.Asc(apikey.FieldUpdatedAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.APIKey, 0, len(rows))
	for _, row := range rows {
		key := apiKeyEntityToService(row)
		if key == nil {
			continue
		}
		out = append(out, *key)
	}
	return out, nil
}

func (r *apiKeyRepository) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.APIKey.Update().
		Where(apikey.UserIDEQ(userID), apikey.GroupIDEQ(oldGroupID), apikey.DeletedAtIsNil()).
		SetGroupID(newGroupID).
		Save(ctx)
	return int64(n), err
}

// CountByGroupID 获取分组的 API Key 数量
func (r *apiKeyRepository) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	count, err := r.activeQuery().Where(apikey.GroupIDEQ(groupID)).Count(ctx)
	return int64(count), err
}

// ListKeyHashesByUserID 返回用户全部未删除 Key 的 key_hash（不是明文）。
// 认证缓存键 = sha256(明文) = key_hash，所以批量失效缓存只需要哈希。
func (r *apiKeyRepository) ListKeyHashesByUserID(ctx context.Context, userID int64) ([]string, error) {
	return r.activeQuery().
		Where(apikey.UserIDEQ(userID)).
		Select(apikey.FieldKeyHash).
		Strings(ctx)
}

// ListKeyHashesByGroupID 返回分组下（含智能路由候选）全部 Key 的 key_hash。
func (r *apiKeyRepository) ListKeyHashesByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	hashes, err := r.activeQuery().
		Where(apikey.GroupIDEQ(groupID)).
		Select(apikey.FieldKeyHash).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	if r.sql == nil {
		return hashes, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT DISTINCT k.key_hash
		FROM api_keys k
		INNER JOIN api_key_group_routes r ON r.api_key_id = k.id
		WHERE r.group_id = $1 AND k.deleted_at IS NULL AND k.key_hash IS NOT NULL
	`, groupID)
	if err != nil {
		if isMissingRelationError(err) {
			return hashes, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seen := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		seen[hash] = struct{}{}
	}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
	}
	return hashes, rows.Err()
}

func (r *apiKeyRepository) ListIDsByUserAndGroup(ctx context.Context, userID, groupID int64) ([]int64, error) {
	return clientFromContext(ctx, r.client).APIKey.Query().
		Where(apikey.UserIDEQ(userID), apikey.GroupIDEQ(groupID), apikey.DeletedAtIsNil()).
		IDs(ctx)
}

// IncrementQuotaUsed 使用 Ent 原子递增 quota_used 字段并返回新值
func (r *apiKeyRepository) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	updated, err := r.client.APIKey.UpdateOneID(id).
		Where(apikey.DeletedAtIsNil()).
		AddQuotaUsed(amount).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return 0, service.ErrAPIKeyNotFound
		}
		return 0, err
	}
	return updated.QuotaUsed, nil
}

// IncrementQuotaUsedAndGetState atomically increments quota_used, conditionally marks the key
// as quota_exhausted, and returns the latest quota state in one round trip.
func (r *apiKeyRepository) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*service.APIKeyQuotaUsageState, error) {
	query := `
		UPDATE api_keys
		SET
			quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0 AND quota_used + $1 >= quota THEN $2
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL
		RETURNING quota_used, quota, COALESCE(key_hash, ''), status
	`

	state := &service.APIKeyQuotaUsageState{}
	if err := scanSingleRow(ctx, r.sql, query, []any{amount, service.StatusAPIKeyQuotaExhausted, id}, &state.QuotaUsed, &state.Quota, &state.KeyHash, &state.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return state, nil
}

func (r *apiKeyRepository) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetLastUsedAt(usedAt).
		SetUpdatedAt(usedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

// BatchUpdateLastUsed persists activity timestamps for multiple API keys in a
// single statement. Authentication treats last_used_at as advisory metadata,
// so the production request path queues these updates in DeferredService
// rather than waiting on one UPDATE per key.
func (r *apiKeyRepository) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	if r == nil || r.sql == nil || len(updates) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(updates))
	for id := range updates {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	times := make([]time.Time, len(ids))
	for i, id := range ids {
		times[i] = updates[id]
	}
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys AS k
		SET last_used_at = GREATEST(COALESCE(k.last_used_at, '-infinity'::timestamptz), v.last_used_at),
		    updated_at = GREATEST(COALESCE(k.updated_at, '-infinity'::timestamptz), v.last_used_at)
		FROM unnest($1::bigint[], $2::timestamptz[]) AS v(id, last_used_at)
		WHERE k.id = v.id AND k.deleted_at IS NULL
		  AND (k.last_used_at IS NULL OR k.last_used_at < v.last_used_at)
	`, pq.Array(ids), pq.Array(times))
	return err
}

// IncrementRateLimitUsage atomically increments all rate limit usage counters and initializes
// window start times via COALESCE if not already set.
func (r *apiKeyRepository) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`,
		cost, id)
	return err
}

// ResetRateLimitWindows resets expired rate limit windows atomically.
func (r *apiKeyRepository) ResetRateLimitWindows(ctx context.Context, id int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN 0 ELSE usage_5h END,
			window_5h_start = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN 0 ELSE usage_1d END,
			window_1d_start = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN 0 ELSE usage_7d END,
			window_7d_start = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`,
		id)
	return err
}

// GetRateLimitData returns the current rate limit usage and window start times for an API key.
func (r *apiKeyRepository) GetRateLimitData(ctx context.Context, id int64) (result *service.APIKeyRateLimitData, err error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start, quota_used
		FROM api_keys
		WHERE id = $1 AND deleted_at IS NULL`,
		id)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		return nil, service.ErrAPIKeyNotFound
	}
	data := &service.APIKeyRateLimitData{}
	if err := rows.Scan(&data.Usage5h, &data.Usage1d, &data.Usage7d, &data.Window5hStart, &data.Window1dStart, &data.Window7dStart, &data.QuotaUsed); err != nil {
		return nil, err
	}
	return data, rows.Err()
}

func apiKeyEntityToService(m *dbent.APIKey) *service.APIKey {
	if m == nil {
		return nil
	}
	out := &service.APIKey{
		ID:               m.ID,
		UserID:           m.UserID,
		Key:              m.Key,
		KeyHash:          m.KeyHash,
		KeyPrefix:        m.KeyPrefix,
		Name:             m.Name,
		Status:           m.Status,
		IPWhitelist:      m.IPWhitelist,
		IPBlacklist:      m.IPBlacklist,
		LastUsedAt:       m.LastUsedAt,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
		KeyRotatedAt:     m.KeyRotatedAt,
		GroupID:          m.GroupID,
		Quota:            m.Quota,
		ConcurrencyLimit: m.ConcurrencyLimit,
		QuotaUsed:        m.QuotaUsed,
		ExpiresAt:        m.ExpiresAt,
		RateLimit5h:      m.RateLimit5h,
		RateLimit1d:      m.RateLimit1d,
		RateLimit7d:      m.RateLimit7d,

		AnthropicCacheTTLMode:        service.NormalizeAnthropicCacheTTLMode(m.AnthropicCacheTTLMode),
		DeepSeekToolInjectionEnabled: &m.DeepseekToolInjectionEnabled,
		AutoBlockDatacenterIPs:       m.AutoBlockDatacenterIps,
		Usage5h:                      m.Usage5h,
		Usage1d:                      m.Usage1d,
		Usage7d:                      m.Usage7d,
		Window5hStart:                m.Window5hStart,
		Window1dStart:                m.Window1dStart,
		Window7dStart:                m.Window7dStart,
	}
	if m.Edges.User != nil {
		out.User = userEntityToService(m.Edges.User)
		if allowed := m.Edges.User.Edges.AllowedGroups; len(allowed) > 0 {
			out.User.AllowedGroups = make([]int64, 0, len(allowed))
			for _, g := range allowed {
				if g != nil {
					out.User.AllowedGroups = append(out.User.AllowedGroups, g.ID)
				}
			}
		}
	}
	if m.Edges.Group != nil {
		out.Group = groupEntityToService(m.Edges.Group)
	}
	return out
}

func userEntityToService(u *dbent.User) *service.User {
	if u == nil {
		return nil
	}
	out := &service.User{
		ID:                                u.ID,
		Email:                             u.Email,
		Username:                          u.Username,
		Notes:                             u.Notes,
		PasswordHash:                      u.PasswordHash,
		Role:                              u.Role,
		Balance:                           u.Balance,
		FrozenBalance:                     u.FrozenBalance,
		Concurrency:                       u.Concurrency,
		Status:                            u.Status,
		SignupSource:                      u.SignupSource,
		LastLoginAt:                       u.LastLoginAt,
		LastActiveAt:                      u.LastActiveAt,
		TotpSecretEncrypted:               u.TotpSecretEncrypted,
		TotpEnabled:                       u.TotpEnabled,
		TotpEnabledAt:                     u.TotpEnabledAt,
		BalanceNotifyEnabled:              u.BalanceNotifyEnabled,
		RestrictPublicGroups:              u.RestrictPublicGroups,
		BalanceNotifyThresholdType:        u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:            u.BalanceNotifyThreshold,
		BalanceNotifyPrimaryEmailDisabled: u.BalanceNotifyPrimaryEmailDisabled,
		TotalRecharged:                    u.TotalRecharged,
		RPMLimit:                          u.RpmLimit,
		CreatedAt:                         u.CreatedAt,
		UpdatedAt:                         u.UpdatedAt,
		DeletedAt:                         u.DeletedAt,
	}
	// Parse extra emails JSON (supports both old []string and new []NotifyEmailEntry format)
	if u.BalanceNotifyExtraEmails != "" && u.BalanceNotifyExtraEmails != "[]" {
		out.BalanceNotifyExtraEmails = service.ParseNotifyEmails(u.BalanceNotifyExtraEmails)
	}
	return out
}

func groupEntityToService(g *dbent.Group) *service.Group {
	if g == nil {
		return nil
	}
	var modelPricing []service.ChannelModelPricing
	if len(g.ModelPricing) > 0 {
		if err := json.Unmarshal(g.ModelPricing, &modelPricing); err != nil {
			slog.Warn("group model_pricing unmarshal failed; falling back to channel/builtin pricing",
				"group_id", g.ID, "error", err)
			modelPricing = nil
		}
	}
	return &service.Group{
		ID:                           g.ID,
		Name:                         g.Name,
		Description:                  derefString(g.Description),
		Platform:                     g.Platform,
		RateMultiplier:               g.RateMultiplier,
		IsExclusive:                  g.IsExclusive,
		Status:                       g.Status,
		Hydrated:                     true,
		DuplicateOperationID:         derefString(g.DuplicateOperationID),
		SubscriptionType:             g.SubscriptionType,
		DailyLimitUSD:                g.DailyLimitUsd,
		WeeklyLimitUSD:               g.WeeklyLimitUsd,
		MonthlyLimitUSD:              g.MonthlyLimitUsd,
		AllowImageGeneration:         g.AllowImageGeneration,
		AllowBatchImageGeneration:    g.AllowBatchImageGeneration,
		ImageRateIndependent:         g.ImageRateIndependent,
		ImageRateMultiplier:          g.ImageRateMultiplier,
		ImagePrice1K:                 g.ImagePrice1k,
		ImagePrice2K:                 g.ImagePrice2k,
		ImagePrice4K:                 g.ImagePrice4k,
		BatchImageDiscountMultiplier: g.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:     g.BatchImageHoldMultiplier,
		VideoRateIndependent:         g.VideoRateIndependent,
		VideoRateMultiplier:          g.VideoRateMultiplier,
		VideoPrice480P:               g.VideoPrice480p,
		VideoPrice720P:               g.VideoPrice720p,
		VideoPrice1080P:              g.VideoPrice1080p,
		VideoModelPrices:             service.NormalizeVideoModelPrices(g.VideoModelPrices),
		WebSearchPricePerCall:        g.WebSearchPricePerCall,
		ToolRateIndependent:          g.ToolRateIndependent,
		ToolRateMultiplier:           g.ToolRateMultiplier,
		SearchPricePer1k:             g.SearchPricePer1k,
		FetchPricePer1k:              g.FetchPricePer1k,
		OCRPricePer1kPages:           g.OcrPricePer1kPages,
		// 搜索专用凭据必须一并映射：执行搜索时用的是这条投影出来的
		// Group，缺了它就会静默回落到账号凭据，分组配置等于没生效。
		SearchProviderBaseURL:           g.SearchProviderBaseURL,
		SearchProviderAPIKey:            g.SearchProviderAPIKey,
		AudioRealtimePricePerMin:        g.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    g.AudioTtsPricePerMillionChars,
		AudioSTTPricePerHour:            g.AudioSttPricePerHour,
		LongContextPricingEnabled:       g.LongContextPricingEnabled,
		ModelPricing:                    modelPricing,
		DefaultValidityDays:             g.DefaultValidityDays,
		ClaudeCodeOnly:                  g.ClaudeCodeOnly,
		FallbackGroupID:                 g.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest,
		ModelRouting:                    g.ModelRouting,
		ModelRoutingEnabled:             g.ModelRoutingEnabled,
		MCPXMLInject:                    g.McpXMLInject,
		SupportedModelScopes:            g.SupportedModelScopes,
		SortOrder:                       g.SortOrder,
		AllowMessagesDispatch:           g.AllowMessagesDispatch,
		CrossProtocolConversionEnabled:  g.CrossProtocolConversionEnabled,
		ReturnRequestedModel:            g.ReturnRequestedModel,
		SaveResponseMsgID:               g.SaveResponseMsgID,
		AllowLive:                       g.AllowLive,
		ForceOpenAIFast:                 g.ForceOpenaiFast,
		FreeOpenAIFast:                  g.FreeOpenaiFast,
		OpenAIFastMultiplier:            g.OpenaiFastMultiplier,
		OpenAIUltrafastMultiplier:       g.OpenaiUltrafastMultiplier,
		RequireOAuthOnly:                g.RequireOauthOnly,
		RequirePrivacySet:               g.RequirePrivacySet,
		DefaultMappedModel:              g.DefaultMappedModel,
		MessagesDispatchModelConfig:     g.MessagesDispatchModelConfig,
		ModelAllowlist:                  service.GroupModelAllowlistFromDomain(g.ModelAllowlist),
		CodexModelsManifestConfig:       g.CodexModelsManifestConfig,
		RPMLimit:                        g.RpmLimit,
		MaxReasoningEffort:              g.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     g.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         g.ReasoningEffortMappings,
		PeakRateEnabled:                 g.PeakRateEnabled,
		PeakStart:                       g.PeakStart,
		PeakEnd:                         g.PeakEnd,
		PeakRateMultiplier:              g.PeakRateMultiplier,
		ProfitControlEnabled:            g.ProfitControlEnabled,
		ProfitMinMargin:                 g.ProfitMinMargin,
		ProfitSafetyBuffer:              g.ProfitSafetyBuffer,
		CreatedAt:                       g.CreatedAt,
		UpdatedAt:                       g.UpdatedAt,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (r *apiKeyRepository) apiKeyEntityToServiceWithRoutes(ctx context.Context, m *dbent.APIKey) (*service.APIKey, error) {
	out := apiKeyEntityToService(m)
	if out == nil {
		return nil, nil
	}
	keys := []service.APIKey{*out}
	if err := r.attachGroupRoutes(ctx, keys); err != nil {
		return nil, err
	}
	out.RouteGroupIDs = keys[0].RouteGroupIDs
	return out, nil
}

type sqlTxBeginner interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

const (
	// Positions live in their own table and the row count follows the user's
	// available groups (issue #494: 36 groups must fit). Two writers replacing
	// the routes of the SAME key can still interleave: the second DELETE runs
	// on a snapshot that predates the first writer's INSERT, so it deletes
	// nothing and the following INSERT trips UNIQUE (api_key_id, position).
	// A bounded retry re-runs the replace on a fresh snapshot and converges to
	// the last writer's list instead of returning a 500.
	groupRouteReplaceAttempts       = 4
	groupRouteReplaceRetryBaseDelay = 5 * time.Millisecond
)

// ReplaceGroupRoutes replaces the ordered smart-routing candidate list of one
// key. There is intentionally no upper bound on len(groupIDs): the position
// column only carries ordering.
func (r *apiKeyRepository) ReplaceGroupRoutes(ctx context.Context, apiKeyID int64, groupIDs []int64) error {
	if r.sql == nil {
		return nil
	}
	beginner, ownsTx := r.sql.(sqlTxBeginner)
	if !ownsTx {
		// Caller owns the transaction. A failed statement aborts that
		// transaction, so retrying here would be meaningless.
		if _, err := r.sql.ExecContext(ctx, "DELETE FROM api_key_group_routes WHERE api_key_id = $1", apiKeyID); err != nil {
			return err
		}
		return insertAPIKeyGroupRoutes(ctx, r.sql, apiKeyID, groupIDs)
	}

	var lastErr error
	for attempt := 0; attempt < groupRouteReplaceAttempts; attempt++ {
		if attempt > 0 {
			if err := waitAPIKeyGroupRouteRetry(ctx, attempt); err != nil {
				return err
			}
		}
		lastErr = replaceAPIKeyGroupRoutesInTx(ctx, beginner, apiKeyID, groupIDs)
		if lastErr == nil {
			return nil
		}
		if !isAPIKeyGroupRoutePositionConflict(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func replaceAPIKeyGroupRoutesInTx(ctx context.Context, beginner sqlTxBeginner, apiKeyID int64, groupIDs []int64) error {
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM api_key_group_routes WHERE api_key_id = $1", apiKeyID); err != nil {
		return err
	}
	if err := insertAPIKeyGroupRoutes(ctx, tx, apiKeyID, groupIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func insertAPIKeyGroupRoutes(ctx context.Context, exec sqlExecutor, apiKeyID int64, groupIDs []int64) error {
	for i, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		if _, err := exec.ExecContext(ctx, `
			INSERT INTO api_key_group_routes (api_key_id, group_id, position)
			VALUES ($1, $2, $3)
		`, apiKeyID, groupID, i+1); err != nil {
			return err
		}
	}
	return nil
}

func waitAPIKeyGroupRouteRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt) * groupRouteReplaceRetryBaseDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// isAPIKeyGroupRoutePositionConflict reports the one race this retry handles:
// UNIQUE (api_key_id, position) taken by a concurrent route replacement.
// Primary-key conflicts (duplicate group ids in one request) are real client
// errors and must surface immediately.
func isAPIKeyGroupRoutePositionConflict(err error) bool {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return false
	}
	return pqErr.Code == "23505" && pqErr.Constraint == "api_key_group_routes_api_key_id_position_key"
}

func (r *apiKeyRepository) ListGroupRoutes(ctx context.Context, apiKeyIDs []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(apiKeyIDs))
	if r.sql == nil || len(apiKeyIDs) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT api_key_id, group_id
		FROM api_key_group_routes
		WHERE api_key_id = ANY($1)
		ORDER BY api_key_id ASC, position ASC
	`, pq.Array(apiKeyIDs))
	if err != nil {
		if isMissingRelationError(err) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var apiKeyID, groupID int64
		if err := rows.Scan(&apiKeyID, &groupID); err != nil {
			return nil, err
		}
		out[apiKeyID] = append(out[apiKeyID], groupID)
	}
	return out, rows.Err()
}

func (r *apiKeyRepository) attachGroupRoutes(ctx context.Context, keys []service.APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		ids = append(ids, keys[i].ID)
	}
	routes, err := r.ListGroupRoutes(ctx, ids)
	if err != nil {
		return err
	}
	for i := range keys {
		keys[i].RouteGroupIDs = routes[keys[i].ID]
	}
	return nil
}
