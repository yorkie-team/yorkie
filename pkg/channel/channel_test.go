package channel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func TestPresenceAttachableInterface(t *testing.T) {
	t.Run("implements Attachable interface", func(t *testing.T) {
		ch, err := channel.New(key.Key("test-presence"))
		assert.NoError(t, err)

		// Verify it implements Attachable interface
		var _ attachable.Attachable = ch

		assert.Equal(t, "test-presence", ch.Key().String())
		assert.Equal(t, attachable.TypeChannel, ch.Type())
		assert.Equal(t, attachable.StatusDetached, ch.Status())
		assert.False(t, ch.IsAttached())
	})

	t.Run("status changes work correctly", func(t *testing.T) {
		ch, err := channel.New(key.Key("test-presence"))
		assert.NoError(t, err)

		assert.Equal(t, attachable.StatusDetached, ch.Status())
		assert.False(t, ch.IsAttached())

		ch.SetStatus(attachable.StatusAttached)
		assert.Equal(t, attachable.StatusAttached, ch.Status())
		assert.True(t, ch.IsAttached())

		ch.SetStatus(attachable.StatusRemoved)
		assert.Equal(t, attachable.StatusRemoved, ch.Status())
		assert.False(t, ch.IsAttached())
	})
}

func TestAttachableInterfaceCompatibility(t *testing.T) {
	t.Run("Document and Presence both implement Attachable", func(t *testing.T) {
		doc := document.New(key.Key("test-doc"))
		ch, err := channel.New(key.Key("test-presence"))
		assert.NoError(t, err)

		for i, resource := range []attachable.Attachable{doc, ch} {
			assert.NotNil(t, resource.Key())
			assert.NotEmpty(t, resource.Type())
			assert.Equal(t, attachable.StatusDetached, resource.Status())
			assert.False(t, resource.IsAttached())

			// Test that each resource has the correct type
			if i == 0 {
				assert.Equal(t, attachable.TypeDocument, resource.Type())
			} else {
				assert.Equal(t, attachable.TypeChannel, resource.Type())
			}
		}
	})

	t.Run("status changes work for both types", func(t *testing.T) {
		doc := document.New(key.Key("test-doc"))
		counter, err := channel.New(key.Key("test-presence"))
		assert.NoError(t, err)

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

	t.Run("channel key path validation", func(t *testing.T) {
		assert.True(t, channel.IsValidChannelKeyPath("room-1"))
		assert.True(t, channel.IsValidChannelKeyPath("room-1.section-1"))
		assert.True(t, channel.IsValidChannelKeyPath("room-1.section-1.user-1"))

		assert.False(t, channel.IsValidChannelKeyPath(""))
		assert.False(t, channel.IsValidChannelKeyPath(" "))
		assert.False(t, channel.IsValidChannelKeyPath("......."))
		assert.False(t, channel.IsValidChannelKeyPath(".room-1"))
		assert.False(t, channel.IsValidChannelKeyPath(".room-1."))
		assert.False(t, channel.IsValidChannelKeyPath("room-1."))
		assert.False(t, channel.IsValidChannelKeyPath("room-1.section-1."))
		assert.False(t, channel.IsValidChannelKeyPath("room-1..section-1"))
	})

	t.Run("parse channel key path", func(t *testing.T) {
		paths, err := channel.ParseKeyPath(key.Key("room-1"))
		assert.NoError(t, err)
		assert.Equal(t, []string{"room-1"}, paths)
		paths, err = channel.ParseKeyPath(key.Key("room-1.section-1"))
		assert.NoError(t, err)
		assert.Equal(t, []string{"room-1", "section-1"}, paths)
		paths, err = channel.ParseKeyPath(key.Key("room-1.section-1.user-1"))
		assert.NoError(t, err)
		assert.Equal(t, []string{"room-1", "section-1", "user-1"}, paths)
	})

	t.Run("first key path is returned correctly", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)
		assert.Equal(t, "room-1", ch.FirstKeyPath())
		ch, err = channel.New(key.Key("room-1.section-1"))
		assert.NoError(t, err)
		assert.Equal(t, "room-1", ch.FirstKeyPath())
		ch, err = channel.New(key.Key("room-1.section-1.user-1"))
		assert.NoError(t, err)
		assert.Equal(t, "room-1", ch.FirstKeyPath())

		firstKeyPath, err := channel.FirstKeyPath(key.Key("room-1"))
		assert.NoError(t, err)
		assert.Equal(t, "room-1", firstKeyPath)
		firstKeyPath, err = channel.FirstKeyPath(key.Key("room-1.section-1"))
		assert.NoError(t, err)
		assert.Equal(t, "room-1", firstKeyPath)
		firstKeyPath, err = channel.FirstKeyPath(key.Key("room-1.section-1.user-1"))
		assert.NoError(t, err)
		assert.Equal(t, "room-1", firstKeyPath)
	})
}
