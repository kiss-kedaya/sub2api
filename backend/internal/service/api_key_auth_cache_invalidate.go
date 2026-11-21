package service

import (
	"context"
	"strings"
)

// InvalidateAuthCacheByKey 清除指定明文 API Key 的认证缓存。
// 只应在手上确实有明文时调用（创建 / 轮换 / 尚未回填哈希的历史行）。
func (s *APIKeyService) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	if key == "" {
		return
	}
	s.deleteAuthCache(ctx, s.authCacheKey(key))
}

// InvalidateAuthCacheByHash 清除指定 key_hash 的认证缓存。
//
// 认证缓存键就是 sha256(明文)，也就是落库的 key_hash（见 authCacheKey）。
// 库里不再有明文之后，这是「按一把已存在的 key 失效缓存」的唯一入口：
// 状态变更、配额打满、分组迁移、风控停用、删除等路径都靠它。
func (s *APIKeyService) InvalidateAuthCacheByHash(ctx context.Context, keyHash string) {
	if !IsAPIKeyHash(keyHash) {
		return
	}
	s.deleteAuthCache(ctx, strings.ToLower(keyHash))
}

// InvalidateAuthCacheForAPIKey 按对象里能拿到的凭据标识失效缓存。
// 优先用 KeyHash（新版行只有它）；历史行可能还带着明文，且明文与哈希不一致时
// （key_hash 尚未回填）再按明文补一刀。两份都清不存在的键删除是安全的。
func (s *APIKeyService) InvalidateAuthCacheForAPIKey(ctx context.Context, apiKey *APIKey) {
	if apiKey == nil {
		return
	}
	if apiKey.KeyHash != "" {
		s.InvalidateAuthCacheByHash(ctx, apiKey.KeyHash)
		if apiKey.Key != "" && HashAPIKey(apiKey.Key) != apiKey.KeyHash {
			s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
		}
		return
	}
	s.InvalidateAuthCacheByKey(ctx, apiKey.Key)
}

// InvalidateAuthCacheByCredential 用于「拿到的可能是 key_hash、也可能是明文」的
// 调用方（删除路径的轻量查询，它只查 COALESCE(key_hash, key) 一种值）。
//
// 这里对两种解释都清一遍：不存在的缓存键删除是安全的，代价只是一次多余的 DEL，
// 但避免去猜「这个 64 位字符串到底是哈希，还是用户自定义的 64 位 hex key」——
// 猜错就会留下一条仍能鉴权的旧缓存。
func (s *APIKeyService) InvalidateAuthCacheByCredential(ctx context.Context, credential string) {
	if credential == "" {
		return
	}
	s.InvalidateAuthCacheByHash(ctx, credential)
	s.InvalidateAuthCacheByKey(ctx, credential)
}

// InvalidateAuthCacheByUserID 清除用户相关的 API Key 认证缓存。
// 批量查询返回的是 key_hash（不再读明文），因此直接按哈希失效。
func (s *APIKeyService) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	if userID <= 0 {
		return
	}
	hashes, err := s.apiKeyRepo.ListKeyHashesByUserID(ctx, userID)
	if err != nil {
		return
	}
	s.deleteAuthCacheByHashes(ctx, hashes)
}

// InvalidateAuthCacheByGroupID 清除分组相关的 API Key 认证缓存。
func (s *APIKeyService) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	if groupID <= 0 {
		return
	}
	hashes, err := s.apiKeyRepo.ListKeyHashesByGroupID(ctx, groupID)
	if err != nil {
		return
	}
	s.deleteAuthCacheByHashes(ctx, hashes)
}

func (s *APIKeyService) deleteAuthCacheByHashes(ctx context.Context, hashes []string) {
	for _, hash := range hashes {
		s.InvalidateAuthCacheByHash(ctx, hash)
	}
}
