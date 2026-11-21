package schema

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

var authProviderTypes = map[string]struct{}{
	"email":    {},
	"github":   {},
	"google":   {},
	"linuxdo":  {},
	"oidc":     {},
	"wechat":   {},
	"dingtalk": {},
}

func validateAuthProviderType(value string) error {
	if _, ok := authProviderTypes[value]; ok {
		return nil
	}
	return fmt.Errorf("invalid auth provider type %q", value)
}

// AuthIdentity stores the canonical login identity for an account.
type AuthIdentity struct {
	ent.Schema
}

func (AuthIdentity) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "auth_identities"},
	}
}

func (AuthIdentity) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (AuthIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.String("provider_type").
			MaxLen(20).
			NotEmpty().
			Validate(validateAuthProviderType),
		field.String("provider_key").
			NotEmpty().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		// provider_subject 是明文回退列：迁移过渡期保留，登录匹配优先走
		// provider_subject_hash；回填校验通过后由运维 SQL 清空为 NULL
		// （见 docs/ops/auth-identity-subject-hash-20261121.md）。
		field.String("provider_subject").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		// provider_subject_hash 是 sha256(subject) 的小写 hex（盲索引），
		// 登录匹配、唯一约束与去重都以它为准。
		field.String("provider_subject_hash").
			Optional().
			Nillable().
			MaxLen(64).
			SchemaType(map[string]string{dialect.Postgres: "char(64)"}),
		// provider_subject_prefix 是后台展示用的短前缀（hash 前 8 位），
		// 完整 subject 不再回显。
		field.String("provider_subject_prefix").
			Optional().
			Nillable().
			MaxLen(16).
			SchemaType(map[string]string{dialect.Postgres: "varchar(16)"}),
		field.Time("verified_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("issuer").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.JSON("metadata", map[string]any{}).
			Default(func() map[string]any { return map[string]any{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
	}
}

func (AuthIdentity) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("auth_identities").
			Field("user_id").
			Required().
			Unique(),
		edge.To("channels", AuthIdentityChannel.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("adoption_decisions", IdentityAdoptionDecision.Type),
	}
}

func (AuthIdentity) Indexes() []ent.Index {
	return []ent.Index{
		// 明文唯一索引保留到「清空明文列」阶段；历史行与过渡期回退查询仍依赖它。
		index.Fields("provider_type", "provider_key", "provider_subject").Unique(),
		// 盲索引唯一约束：provider_key 保留原有「provider 实例」语义
		// （微信/多租户 OIDC 下 provider_key 是隔离维度）。NULL 不参与唯一约束，
		// 因此回填前的历史行不会互相冲突。
		index.Fields("provider_type", "provider_key", "provider_subject_hash").Unique(),
		// 只按 (provider_type, subject) 查询的兼容路径（如 pending 流程的存在性检查）。
		index.Fields("provider_type", "provider_subject_hash"),
		index.Fields("user_id"),
		index.Fields("user_id", "provider_type"),
	}
}
