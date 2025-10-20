package presence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/pkg/presence"
)

func TestPresenceAttachableInterface(t *testing.T) {
	t.Run("implements Attachable interface", func(t *testing.T) {
		p := presence.New(key.Key("test-presence"))

		// Verify it implements Attachable interface
		var _ attachable.Attachable = p

		assert.Equal(t, "test-presence", p.Key().String())
		assert.Equal(t, attachable.TypePresence, p.Type())
		assert.Equal(t, attachable.StatusDetached, p.Status())
		assert.False(t, p.IsAttached())
	})

	t.Run("status changes work correctly", func(t *testing.T) {
		p := presence.New(key.Key("test-presence"))

		assert.Equal(t, attachable.StatusDetached, p.Status())
		assert.False(t, p.IsAttached())

		p.SetStatus(attachable.StatusAttached)
		assert.Equal(t, attachable.StatusAttached, p.Status())
		assert.True(t, p.IsAttached())

		p.SetStatus(attachable.StatusRemoved)
		assert.Equal(t, attachable.StatusRemoved, p.Status())
		assert.False(t, p.IsAttached())
	})
}

func TestAttachableInterfaceCompatibility(t *testing.T) {
	t.Run("Document and Presence both implement Attachable", func(t *testing.T) {
		doc := document.New(key.Key("test-doc"))
		p := presence.New(key.Key("test-presence"))

		for i, resource := range []attachable.Attachable{doc, p} {
			assert.NotNil(t, resource.Key())
			assert.NotEmpty(t, resource.Type())
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())

			// Test that each resource has the correct type
			if i == 0 {
				assert.Equal(t, attachable.TypeDocument, resource.Type())
			} else {
				assert.Equal(t, attachable.TypePresence, resource.Type())
			}
		}
	})

	t.Run("status changes work for both types", func(t *testing.T) {
		doc := document.New(key.Key("test-doc"))
		counter := presence.New(key.Key("test-presence"))

		for _, resource := range []attachable.Attachable{doc, counter} {
			resource.SetStatus(attachable.StatusAttached)
			assert.Equal(t, attachable.StatusAttached, resource.Status())
			assert.True(t, resource.IsAttached())

			resource.SetStatus(attachable.StatusRemoved)
			assert.Equal(t, attachable.StatusRemoved, resource.Status())
			assert.False(t, resource.IsAttached())

			resource.SetStatus(attachable.StatusDetached)
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())
		}
	})
}
