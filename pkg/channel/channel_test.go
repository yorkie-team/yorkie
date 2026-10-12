/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package channel_test

import (
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func TestChannelAttachableInterface(t *testing.T) {
	t.Run("implements Attachable interface", func(t *testing.T) {
		ch, err := channel.New(key.Key("test-channel"))
		assert.NoError(t, err)

		// Verify it implements Attachable interface
		var _ attachable.Attachable = ch

		assert.Equal(t, "test-channel", ch.Key().String())
		assert.Equal(t, attachable.TypeChannel, ch.Type())
		assert.Equal(t, attachable.StatusDetached, ch.Status())
		assert.False(t, ch.IsAttached())
	})

	t.Run("status changes work correctly", func(t *testing.T) {
		ch, err := channel.New(key.Key("test-channel"))
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
	t.Run("Document and Channel both implement Attachable", func(t *testing.T) {
		doc := document.New(key.Key("test-doc"))
		ch, err := channel.New(key.Key("test-channel"))
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
		ch, err := channel.New(key.Key("test-channel"))
		assert.NoError(t, err)

		for _, resource := range []attachable.Attachable{doc, ch} {
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

func TestChannelBroadcastServing(t *testing.T) {
	t.Run("unserviced channel does not block test", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)

		done := make(chan error, 1)
		go func() { done <- ch.Broadcast("topic", "payload") }()

		select {
		case err := <-done:
			assert.ErrorIs(t, err, channel.ErrBroadcastUnavailable)
		case <-gotime.After(3 * gotime.Second):
			t.Fatal("Broadcast blocked with no servicer")
		}
	})

	t.Run("retired servicer releases a waiting broadcast test", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)

		// The servicer takes the request and then retires without answering,
		// which is what cancelling the attachment's watch context does.
		token, ok := ch.StartBroadcastServing()
		assert.True(t, ok)
		stop := make(chan struct{})
		go func() {
			<-token.Requests()
			<-stop
			ch.StopBroadcastServing(token)
		}()

		done := make(chan error, 1)
		go func() { done <- ch.Broadcast("topic", "payload") }()

		close(stop)
		select {
		case err := <-done:
			assert.ErrorIs(t, err, channel.ErrBroadcastUnavailable)
		case <-gotime.After(3 * gotime.Second):
			t.Fatal("Broadcast blocked after its servicer retired")
		}
	})

	t.Run("live servicer answers broadcast test", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)

		token, ok := ch.StartBroadcastServing()
		assert.True(t, ok)
		defer ch.StopBroadcastServing(token)
		go func() {
			r := <-token.Requests()
			assert.Equal(t, "topic", r.Topic)
			assert.True(t, ch.SendBroadcastResponse(token, nil))
		}()

		assert.NoError(t, ch.Broadcast("topic", "payload"))
	})

	t.Run("second servicer is refused while the first holds the claim test", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)

		first, ok := ch.StartBroadcastServing()
		assert.True(t, ok)

		_, ok = ch.StartBroadcastServing()
		assert.False(t, ok, "a second servicer took the claim of a live one")

		// The claim is grantable again once its holder retires, which is what
		// a watch reopened after a close or a detach relies on.
		ch.StopBroadcastServing(first)
		second, ok := ch.StartBroadcastServing()
		assert.True(t, ok)
		ch.StopBroadcastServing(second)
	})

	t.Run("retired servicer does not retire its successor test", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)

		first, ok := ch.StartBroadcastServing()
		assert.True(t, ok)
		ch.StopBroadcastServing(first)

		second, ok := ch.StartBroadcastServing()
		assert.True(t, ok)
		defer ch.StopBroadcastServing(second)

		// The first servicer winding down late must not take the second one's
		// claim with it, nor land its answer on the second one's caller.
		ch.StopBroadcastServing(first)
		assert.False(t, ch.SendBroadcastResponse(first, assert.AnError))

		go func() {
			<-second.Requests()
			assert.True(t, ch.SendBroadcastResponse(second, nil))
		}()

		done := make(chan error, 1)
		go func() { done <- ch.Broadcast("topic", "payload") }()

		select {
		case err := <-done:
			assert.NoError(t, err, "a live watch reported its broadcast as unavailable")
		case <-gotime.After(3 * gotime.Second):
			t.Fatal("Broadcast blocked while its servicer was live")
		}
	})

	t.Run("retiring servicer does not take its successor's request test", func(t *testing.T) {
		ch, err := channel.New(key.Key("room-1"))
		assert.NoError(t, err)

		first, ok := ch.StartBroadcastServing()
		assert.True(t, ok)

		// The first servicer is still reading when its claim is released and
		// the successor's is granted -- which is what a close followed by a
		// rewatch does. A request it takes here is one it cannot answer, and
		// the Broadcast that sent it would wait for an answer forever.
		taken := make(chan channel.BroadcastRequest, 1)
		go func() {
			select {
			case r := <-first.Requests():
				taken <- r
			case <-first.Done():
			}
		}()

		ch.StopBroadcastServing(first)
		second, ok := ch.StartBroadcastServing()
		assert.True(t, ok)
		defer ch.StopBroadcastServing(second)

		go func() {
			r := <-second.Requests()
			assert.Equal(t, "topic", r.Topic)
			assert.True(t, ch.SendBroadcastResponse(second, nil))
		}()

		done := make(chan error, 1)
		go func() { done <- ch.Broadcast("topic", "payload") }()

		select {
		case err := <-done:
			assert.NoError(t, err, "the successor's broadcast went unanswered")
		case <-gotime.After(3 * gotime.Second):
			t.Fatal("Broadcast blocked after the serving claim changed hands")
		}
		assert.Len(t, taken, 0, "the retiring servicer took its successor's request")
	})
}
