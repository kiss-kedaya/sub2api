package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Setting holds the schema definition for the Setting entity.
type Setting struct {
	ent.Schema
}

func (Setting) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "settings"},
	}
}

func (Setting) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").
			MaxLen(100).
			NotEmpty().
			Unique(),
		field.String("value").
			NotEmpty().
			SchemaType(map[string]string{
				dialect.Postgres: "text",
			}),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			SchemaType(map[string]string{
				dialect.Postgres: "timestamptz",
			}),
	}
}

func (Setting) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Unique(),
	}
}
