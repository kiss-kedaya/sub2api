package service

import (
	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// clientDialect 返回 ent client 底层数据库方言；client 或 driver 缺失时返回空串。
// 需要按方言拼 raw SQL 的地方统一用它，避免各写一套。
func clientDialect(client *dbent.Client) string {
	if client == nil || client.Driver() == nil {
		return ""
	}
	return client.Driver().Dialect()
}
