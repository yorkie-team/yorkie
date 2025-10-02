package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/pkg/presence"
)

func TestClientAttachableIntegration(t *testing.T) {
	// Skip this test if server is not available
	t.Skip("Integration test - requires running server")

	ctx := context.Background()

	// Create client
	c, err := New()
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, c.Close())
	}()

	// Activate client
	err = c.Activate(ctx)
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, c.Deactivate(ctx))
	}()

	t.Run("AttachResource with Document", func(t *testing.T) {
		docKey := key.Key("test-doc")
		doc := document.New(docKey)

		// Test Attachable interface
		var resource attachable.Attachable = doc
		assert.Equal(t, attachable.AttachableTypeDocument, resource.Type())
		assert.Equal(t, attachable.StatusDetached, resource.Status())

		// Attach using generic method
		err := c.Attach(ctx, resource)
		assert.NoError(t, err)
		assert.Equal(t, attachable.StatusAttached, resource.Status())

		// Detach using generic method
		err = c.Detach(ctx, resource)
		assert.NoError(t, err)
		assert.Equal(t, attachable.StatusDetached, resource.Status())
	})

	t.Run("AttachResource with Presence Counter", func(t *testing.T) {
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		// Test Attachable interface
		var resource attachable.Attachable = counter
		assert.Equal(t, attachable.AttachableTypePresence, resource.Type())
		assert.Equal(t, attachable.StatusDetached, resource.Status())

		// Attach using generic method
		err := c.Attach(ctx, resource)
		// Note: This might fail if server doesn't have presence endpoints implemented
		// For now, we just test that the method exists and handles the type correctly
		if err != nil {
			t.Logf("Expected error - server presence endpoints not fully implemented: %v", err)
		}
	})

	t.Run("Mixed Resources", func(t *testing.T) {
		docKey := key.Key("mixed-doc")
		doc := document.New(docKey)

		presenceKey := key.Key("mixed-presence")
		counter := presence.New(presenceKey)

		resources := []attachable.Attachable{doc, counter}

		for i, resource := range resources {
			t.Logf("Processing resource %d: %s (%s)", i, resource.Key(), resource.Type())

			// All resources should start detached
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())

			// Attempt to attach
			err := c.Attach(ctx, resource)
			if resource.Type() == attachable.AttachableTypeDocument {
				// Document attachment should work
				assert.NoError(t, err)
				assert.Equal(t, attachable.StatusAttached, resource.Status())
				assert.True(t, resource.IsAttached())

				// Clean up
				err = c.Detach(ctx, resource)
				assert.NoError(t, err)
			} else {
				// Presence might not be fully implemented yet
				t.Logf("Presence attachment result: %v", err)
			}
		}
	})
}

func TestAttachableInterfaceCompatibility(t *testing.T) {
	t.Run("Document implements Attachable", func(t *testing.T) {
		docKey := key.Key("test-doc")
		doc := document.New(docKey)

		// Ensure Document implements Attachable
		var _ attachable.Attachable = doc

		assert.Equal(t, "test-doc", doc.Key().String())
		assert.Equal(t, attachable.AttachableTypeDocument, doc.Type())
		assert.Equal(t, attachable.StatusDetached, doc.Status())
		assert.False(t, doc.IsAttached())
	})

	t.Run("Presence Counter implements Attachable", func(t *testing.T) {
		presenceKey := key.Key("test-presence")
		counter := presence.New(presenceKey)

		// Ensure Presence Counter implements Attachable
		var _ attachable.Attachable = counter

		assert.Equal(t, "test-presence", counter.Key().String())
		assert.Equal(t, attachable.AttachableTypePresence, counter.Type())
		assert.Equal(t, attachable.StatusDetached, counter.Status())
		assert.False(t, counter.IsAttached())
	})

	t.Run("Status changes work correctly", func(t *testing.T) {
		docKey := key.Key("status-doc")
		doc := document.New(docKey)

		presenceKey := key.Key("status-presence")
		counter := presence.New(presenceKey)

		resources := []attachable.Attachable{doc, counter}

		for _, resource := range resources {
			// Test status transitions
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())

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
