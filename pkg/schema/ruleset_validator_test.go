package schema_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/schema"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRulesetValidator(t *testing.T) {
	t.Run("validate primitive type test", func(t *testing.T) {
		ruleset := []types.Rule{
			{Path: "$.field1", Type: "null"},
			{Path: "$.field2", Type: "boolean"},
			{Path: "$.field3", Type: "integer"},
			{Path: "$.field4", Type: "double"},
			{Path: "$.field5", Type: "long"},
			{Path: "$.field6", Type: "string"},
			{Path: "$.field7", Type: "date"},
			{Path: "$.field8", Type: "bytes"},
		}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNull("field1")
			r.SetBool("field2", true)
			r.SetInteger("field3", 123)
			r.SetDouble("field4", 123.456)
			r.SetLong("field5", 9223372036854775807)
			r.SetString("field6", "test")
			r.SetDate("field7", time.Now())
			r.SetBytes("field8", []byte{1, 2, 3})
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.True(t, result.Valid)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetBool("field1", false)
			r.SetInteger("field2", 123)
			r.SetDouble("field3", 123.456)
			r.SetLong("field4", 9223372036854775807)
			r.SetString("field5", "test")
			r.SetDate("field6", time.Now())
			r.SetBytes("field7", []byte{1, 2, 3})
			r.SetNull("field8")
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{
			{Path: "$.field1", Message: "expected null at path $.field1"},
			{Path: "$.field2", Message: "expected boolean at path $.field2"},
			{Path: "$.field3", Message: "expected integer at path $.field3"},
			{Path: "$.field4", Message: "expected double at path $.field4"},
			{Path: "$.field5", Message: "expected long at path $.field5"},
			{Path: "$.field6", Message: "expected string at path $.field6"},
			{Path: "$.field7", Message: "expected date at path $.field7"},
			{Path: "$.field8", Message: "expected bytes at path $.field8"},
		}, result.Errors)
	})

	t.Run("validate object type test", func(t *testing.T) {
		ruleset := []types.Rule{
			{Path: "$.user", Type: "object"},
		}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewObject("user").SetString("name", "test")
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.True(t, result.Valid)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("user", "not an object")
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{{
			Path:    "$.user",
			Message: "expected object at path $.user",
		}}, result.Errors)
	})

	t.Run("validate array type test", func(t *testing.T) {
		ruleset := []types.Rule{{Path: "$.items", Type: "array"}}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewArray("items").AddInteger(1, 2, 3)
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.True(t, result.Valid)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("items", "not an array")
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{{
			Path:    "$.items",
			Message: "expected array at path $.items",
		}}, result.Errors)
	})

	t.Run("validate nested paths test", func(t *testing.T) {
		ruleset := []types.Rule{
			{Path: "$", Type: "object"},
			{Path: "$.user", Type: "object"},
			{Path: "$.user.name", Type: "string"},
			{Path: "$.user.age", Type: "integer"},
		}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewObject("user").SetString("name", "test").SetInteger("age", 25)
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.True(t, result.Valid)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetObject("user").SetInteger("name", 123)
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{{
			Path:    "$.user.name",
			Message: "expected string at path $.user.name",
		}}, result.Errors)
	})

	t.Run("handle yorkie types test", func(t *testing.T) {
		ruleset := []types.Rule{
			{Path: "$", Type: "object"},
			{Path: "$.text", Type: "yorkie.Text"},
			{Path: "$.tree", Type: "yorkie.Tree"},
			{Path: "$.counter", Type: "yorkie.Counter"},
		}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("text")
			r.SetNewTree("tree", json.TreeNode{Type: "doc"})
			r.SetNewCounter("counter", crdt.IntegerCnt, 0)
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.True(t, result.Valid)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("text", "text")
			r.SetString("tree", "doc")
			r.SetInteger("counter", 1)
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{
			{
				Path:    "$.text",
				Message: "expected yorkie.Text at path $.text",
			},
			{
				Path:    "$.tree",
				Message: "expected yorkie.Tree at path $.tree",
			},
			{
				Path:    "$.counter",
				Message: "expected yorkie.Counter at path $.counter",
			},
		}, result.Errors)
	})
}
