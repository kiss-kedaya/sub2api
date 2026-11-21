package schema

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RedeemCode holds the schema definition for the RedeemCode entity.
//
// 删除策略：硬删除
// RedeemCode 使用硬删除而非软删除，原因如下：
//   - 兑换码具有一次性使用特性，删除后无需保留历史记录
//   - 已使用的兑换码通过 status 和 used_at 字段追踪，无需依赖软删除
//   - 减少数据库存储压力和查询复杂度
//
// 如需审计已删除的兑换码，建议在删除前将关键信息写入审计日志表。
type RedeemCode struct {
	ent.Schema
}

func (RedeemCode) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "redeem_codes"},
	}
}

func (RedeemCode) Fields() []ent.Field {
	return []ent.Field{
		// code 是历史明文列。迁移 328 之后只写 NULL，卡密本体以 code_hash 的
		// 形式保存，明文只在「生成」的当次响应里出现一次。保留 Unique() 是为了
		// 与库里的唯一约束保持 schema 对齐；NULL 不受唯一约束限制。
		field.String("code").
			MaxLen(32).
			Optional().
			Unique().
			Comment("Legacy plaintext redeem code; NULL for hash-only rows"),
		// code_hash = sha256(明文 code) 的小写 hex。校验/兑换只按这一列等值查询。
		field.String("code_hash").
			MaxLen(64).
			Optional().
			Unique().
			Comment("Lowercase hex SHA-256 of the redeem code; the only stored credential"),
		// code_prefix 是展示用前缀（前 8 位）。库里没有明文之后，后台列表/搜索/
		// 日志/审计只能靠它识别「这是哪张卡」。
		field.String("code_prefix").
			MaxLen(16).
			Optional().
			Comment("Display prefix (first 8 characters) of the redeem code"),
		field.String("type").
			MaxLen(20).
			Default(domain.RedeemTypeBalance),
		field.Float("value").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),
		field.String("status").
			MaxLen(20).
			Default(domain.StatusUnused),
		field.Int64("used_by").
			Optional().
			Nillable(),
		field.Time("used_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("notes").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int64("group_id").
			Optional().
			Nillable(),
		field.Int("validity_days").
			Default(30),
	}
}

func (RedeemCode) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("redeem_codes").
			Field("used_by").
			Unique(),
		edge.From("group", Group.Type).
			Ref("redeem_codes").
			Field("group_id").
			Unique(),
	}
}

func (RedeemCode) Indexes() []ent.Index {
	return []ent.Index{
		// code 字段已在 Fields() 中声明 Unique()，无需重复索引
		index.Fields("status"),
		index.Fields("used_by"),
		index.Fields("group_id"),
		index.Fields("expires_at"),
		// 后台按前缀搜索卡密（哈希列已由 Unique() 建索引）。
		index.Fields("code_prefix"),
	}
}
