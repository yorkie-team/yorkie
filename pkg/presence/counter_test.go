package presence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/pkg/presence"
)

func TestPresenceCounterAttachableInterface(t *testing.T) {
	t.Run("implements Attachable interface", func(t *testing.T) {
		// Test Presence Counter implements Attachable interface
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		// Verify it implements Attachable interface
		var _ attachable.Attachable = counter

		// Test interface methods
		assert.Equal(t, "test-presence", counter.Key().String())
		assert.Equal(t, attachable.TypePresence, counter.Type())
		assert.Equal(t, attachable.StatusDetached, counter.Status())
		assert.False(t, counter.IsAttached())
	})

	t.Run("status changes work correctly", func(t *testing.T) {
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		// Initially detached
		assert.Equal(t, attachable.StatusDetached, counter.Status())
		assert.False(t, counter.IsAttached())

		// Set to attached
		counter.SetStatus(attachable.StatusAttached)
		assert.Equal(t, attachable.StatusAttached, counter.Status())
		assert.True(t, counter.IsAttached())

		// Set to removed
		counter.SetStatus(attachable.StatusRemoved)
		assert.Equal(t, attachable.StatusRemoved, counter.Status())
		assert.False(t, counter.IsAttached())
	})

	t.Run("counter operations work correctly", func(t *testing.T) {
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		// Initial count should be 0
		assert.Equal(t, int64(0), counter.Count())

		// Test internal count setting (simulates server updates)
		// This reflects how the counter will be updated by the server
		// rather than direct client manipulation

		// Note: In the real implementation, count changes would come from
		// server responses via AttachPresence/DetachPresence calls
		// and updates through the watch stream

		// For now, we can only test that Count() returns the correct value
		assert.Equal(t, int64(0), counter.Count())
	})
}

func TestAttachableInterfaceCompatibility(t *testing.T) {
	t.Run("Document and Presence both implement Attachable", func(t *testing.T) {
		// Create instances
		docKey := key.Key("test-doc")
		doc := document.New(docKey)
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		// Test interface usage
		resources := []attachable.Attachable{doc, counter}

		for i, resource := range resources {
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
		docKey := key.Key("test-doc")
		doc := document.New(docKey)
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		resources := []attachable.Attachable{doc, counter}

		for _, resource := range resources {
			// Test status transitions
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
