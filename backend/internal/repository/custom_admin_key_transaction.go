package repository

import (
	"context"
	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// accountTransactionScope 同时切换 ORM、原生 SQL 和关联查询，禁止在提交前发布缓存。
func (r *accountRepository) accountTransactionScope(ctx context.Context) *accountRepository {
	tx := dbent.TxFromContext(ctx)
	if tx == nil || r.client == tx.Client() {
		return r
	}
	// 不复制 repository 结构体：本仓库的 accountRepository 含 sync.Mutex 与
	// singleflight.Group（不可复制），且事务作用域不需要调度器缓存。
	return &accountRepository{
		proxyVault:             r.proxyVault,
		credentialVault:        r.credentialVault,
		credentialRewrite:      r.credentialRewrite,
		credentialRewriteCAS:   r.credentialRewriteCAS,
		client:                 tx.Client(),
		sql:                    tx.Client(),
		modelAvailabilityCache: r.modelAvailabilityCache,
	}
}
