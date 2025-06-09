package schema_test

import (
	"testing"

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
	t.Run("validate string type test", func(t *testing.T) {
		ruleset := []types.Rule{{Path: "$.name", Type: "string"}}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("name", "test")
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.True(t, result.Valid)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("name", 123)
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{{
			Path:    "$.name",
			Message: "Expected string at path $.name",
		}}, result.Errors)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.Delete("name")
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{{
			Path:    "$.name",
			Message: "Expected string at path $.name",
		}}, result.Errors)
	})

	t.Run("validate object type test", func(t *testing.T) {
		ruleset := []types.Rule{{Path: "$.user", Type: "object"}}
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
			Message: "Expected object at path $.user",
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
			Message: "Expected array at path $.items",
		}}, result.Errors)
	})

	t.Run("validate nested paths test", func(t *testing.T) {
		ruleset := []types.Rule{
			{Path: "$.user", Type: "object"},
			{Path: "$.user.name", Type: "string"},
			{Path: "$.user.age", Type: "string"},
		}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewObject("user").SetString("name", "test").SetString("age", "25")
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
			Message: "Expected string at path $.user.name",
		}}, result.Errors)
	})

	t.Run("handle missing or unexpected values test", func(t *testing.T) {
		ruleset := []types.Rule{
			{Path: "$.user", Type: "object"},
			{Path: "$.user.name", Type: "string"},
			{Path: "$.user.age", Type: "string"},
		}
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewObject("user").SetString("name", "test")
			return nil
		}))
		result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
		assert.Equal(t, []schema.ValidationError{{
			Path:    "$.user.age",
			Message: "Expected string at path $.user.age",
		}}, result.Errors)

		t.Skip("TODO(chacha912): Implement unexpected values handling.")
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetObject("user").SetString("age", "25").SetString("address", "123 Main St")
			return nil
		}))
		result = schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
		assert.False(t, result.Valid)
	})

	t.Run("handle yorkie types test", func(t *testing.T) {
		ruleset := []types.Rule{
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
				Message: "Expected yorkie.Text at path $.text",
			},
			{
				Path:    "$.tree",
				Message: "Expected yorkie.Tree at path $.tree",
			},
			{
				Path:    "$.counter",
				Message: "Expected yorkie.Counter at path $.counter",
			},
		}, result.Errors)
	})
}
