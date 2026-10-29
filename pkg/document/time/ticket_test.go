/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package time_test

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestTicket(t *testing.T) {
	t.Run("get base64 from actorId of ticket test", func(t *testing.T) {
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(0, 0, actorID)
		assert.Equal(t, actorID.StringBase64(), ticket.ActorIDBase64())
	})

	t.Run("get bytes from actorId of ticket test", func(t *testing.T) {
		hexString := "0123456789abcdef01234567"
		actorID, _ := time.ActorIDFromHex(hexString)
		ticket := time.NewTicket(0, 0, actorID)
		assert.Equal(t, actorID.Bytes(), ticket.ActorIDBytes())
	})

	t.Run("constructor and getter method test", func(t *testing.T) {
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(0, 1, actorID)
		assert.Equal(t, int64(0), ticket.Lamport())
		assert.Equal(t, uint32(1), ticket.Delimiter())
		assert.Equal(t, actorID, ticket.ActorID())

		assert.Equal(t, "0:1:"+ticket.ActorIDBase64(), ticket.Key())
		assert.Equal(t, "0:1:"+ticket.ActorIDBase64()[14:16],
			ticket.ToTestString())
	})

	t.Run("set actorID test", func(t *testing.T) {
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(1, 2, time.InitialActorID).SetActorID(actorID)

		assert.Equal(t, actorID, ticket.ActorID())
		assert.Equal(t, actorID.StringBase64(), ticket.ActorIDBase64())
		assert.Equal(t, "1:2:"+actorID.StringBase64(), ticket.Key())
	})

	t.Run("concurrent read of a shared ticket test", func(t *testing.T) {
		// InitialTicket and MaxTicket are shared by every document in the
		// process: `go test -race` fails here if reading them is not
		// race-free.
		for _, ticket := range []*time.Ticket{time.InitialTicket, time.MaxTicket} {
			base64 := ticket.ActorID().StringBase64()
			key := strconv.FormatInt(ticket.Lamport(), 10) + ":" +
				strconv.FormatUint(uint64(ticket.Delimiter()), 10) + ":" + base64

			var wg sync.WaitGroup
			for range 10 {
				wg.Go(func() {
					assert.Equal(t, key, ticket.Key())
					assert.Equal(t, base64, ticket.ActorIDBase64())
				})
			}
			wg.Wait()
		}
	})

	t.Run("concurrent read of a fresh ticket test", func(t *testing.T) {
		// Any ticket can end up shared, e.g. through a DeepCopy of a cached
		// document, so the first read of a cold ticket must be race-free:
		// `go test -race` fails here if Key or ActorIDBase64 fills its cache
		// with a plain write.
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		ticket := time.NewTicket(7, 3, actorID)
		base64 := actorID.StringBase64()

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				assert.Equal(t, "7:3:"+base64, ticket.Key())
				assert.Equal(t, base64, ticket.ActorIDBase64())
			})
		}
		wg.Wait()
	})

	t.Run("warmed key cache keeps deep equality test", func(t *testing.T) {
		// Tests across the repo compare two tickets that were built
		// independently, e.g. the removedAt of the same node in two client
		// documents, with assert.Equal, which falls through to
		// reflect.DeepEqual. The key cache must not make them differ: a
		// *string cache would compare by the address of its box.
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		one := time.NewTicket(7, 3, actorID)
		other := time.NewTicket(7, 3, actorID)

		assert.Equal(t, one, other)
		assert.Equal(t, one.Key(), other.Key())
		assert.Equal(t, one, other)
	})

	t.Run("ticket comparing test", func(t *testing.T) {
		beforeActorID, _ := time.ActorIDFromHex("0000000000abcdef01234567")
		afterActorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")

		before := time.NewTicket(0, 0, beforeActorID)
		after := time.NewTicket(1, 0, afterActorID)
		assert.True(t, after.After(before))
		assert.False(t, before.After(after))

		before = time.NewTicket(0, 0, beforeActorID)
		after = time.NewTicket(0, 0, afterActorID)
		assert.True(t, after.After(before))
		assert.False(t, before.After(after))

		before = time.NewTicket(0, 0, beforeActorID)
		after = time.NewTicket(0, 1, beforeActorID)
		assert.True(t, after.After(before))
		assert.False(t, before.After(after))

		assert.False(t, before.After(before))
	})
}
