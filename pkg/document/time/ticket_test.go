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
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		tickets := []*time.Ticket{
			time.InitialTicket,
			time.MaxTicket,
			time.NewTicket(1, 2, actorID),
		}

		// Every read of a shared ticket must be a read, not a lazily
		// initializing write: `go test -race` fails here otherwise.
		for _, ticket := range tickets {
			var wg sync.WaitGroup
			keys := make([]string, 10)
			base64s := make([]string, 10)

			for i := range keys {
				wg.Add(1)
				go func() {
					defer wg.Done()
					keys[i] = ticket.Key()
					base64s[i] = ticket.ActorIDBase64()
				}()
			}
			wg.Wait()

			for i := range keys {
				assert.Equal(t, keys[0], keys[i])
				assert.Equal(t, base64s[0], base64s[i])
			}
			assert.Equal(t, ticket.ActorID().StringBase64(), base64s[0])
		}
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
