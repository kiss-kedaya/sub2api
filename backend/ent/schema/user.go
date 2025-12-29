package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// User holds the schema definition for the User entity.
type User struct {
	ent.Schema
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "users"},
	}
}

func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").
			MaxLen(255).
			NotEmpty().
			Unique(),
		field.String("password_hash").
			MaxLen(255).
			NotEmpty(),
		field.String("role").
			MaxLen(20).
			Default(service.RoleUser),
		field.Float("balance").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Default(0),
		field.Int("concurrency").
			Default(5),
		field.String("status").
			MaxLen(20).
			Default(service.StatusActive),

		// Optional profile fields (added later; default '' in DB migration)
		field.String("username").
			MaxLen(100).
			Default(""),
		field.String("wechat").
			MaxLen(100).
			Default(""),
		field.String("notes").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("api_keys", ApiKey.Type),
		edge.To("redeem_codes", RedeemCode.Type),
		edge.To("subscriptions", UserSubscription.Type),
		edge.To("assigned_subscriptions", UserSubscription.Type),
		edge.To("allowed_groups", Group.Type).
			Through("user_allowed_groups", UserAllowedGroup.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("email").Unique(),
		index.Fields("status"),
		index.Fields("deleted_at"),
	}
}
