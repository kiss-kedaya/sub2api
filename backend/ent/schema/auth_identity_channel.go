package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuthIdentityChannel stores channel-scoped identifiers for a canonical identity.
type AuthIdentityChannel struct {
	ent.Schema
}

func (AuthIdentityChannel) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "auth_identity_channels"},
	}
}

func (AuthIdentityChannel) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (AuthIdentityChannel) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("identity_id"),
		field.String("provider_type").
			MaxLen(20).
			NotEmpty().
			Validate(validateAuthProviderType),
		field.String("provider_key").
			NotEmpty().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.String("channel").
			MaxLen(20).
			NotEmpty(),
		field.String("channel_app_id").
			NotEmpty().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		// channel_subject 是明文回退列：过渡期保留，登录/绑定匹配优先走
		// channel_subject_hash；回填校验通过后由运维 SQL 清空为 NULL。
		field.String("channel_subject").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		// channel_subject_hash 是 HMAC-SHA256(安装密钥, channel|type|subject) 的
		// 小写 hex（keyed 盲索引），去重与查询都以它为准。
		field.String("channel_subject_hash").
			Optional().
			Nillable().
			MaxLen(64).
			SchemaType(map[string]string{dialect.Postgres: "char(64)"}),
		// channel_subject_prefix 是后台展示用短前缀，完整 subject 不再回显。
		field.String("channel_subject_prefix").
			Optional().
			Nillable().
			MaxLen(16).
			SchemaType(map[string]string{dialect.Postgres: "varchar(16)"}),
		field.JSON("metadata", map[string]any{}).
			Default(func() map[string]any { return map[string]any{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
	}
}

func (AuthIdentityChannel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("identity", AuthIdentity.Type).
			Ref("channels").
			Field("identity_id").
			Required().
			Unique(),
	}
}

func (AuthIdentityChannel) Indexes() []ent.Index {
	return []ent.Index{
		// 明文唯一索引保留到「清空明文列」阶段；历史行与过渡期回退查询仍依赖它。
		index.Fields("provider_type", "provider_key", "channel", "channel_app_id", "channel_subject").Unique(),
		// keyed 盲索引唯一约束：NULL 不参与唯一约束，回填前的历史行不会冲突。
		index.Fields("provider_type", "provider_key", "channel", "channel_app_id", "channel_subject_hash").Unique(),
		// 只按 (provider_type, channel_subject_hash) 的兼容查询索引。
		index.Fields("provider_type", "channel_subject_hash"),
		index.Fields("identity_id"),
	}
}
