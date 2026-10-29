/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package crdt_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestRGATreeSplit(t *testing.T) {
	t.Run("compare nil id panic test", func(t *testing.T) {
		id := crdt.NewRGATreeSplitNodeID(time.InitialTicket, 0)
		assert.Panics(t, func() { id.Compare(nil) }, "ID cannot be null")
	})

	t.Run("concurrent read of the shared initial node ID test", func(t *testing.T) {
		// The head of every Text shares one node ID: `go test -race` fails
		// here if reading it is not race-free.
		key := time.InitialTicket.Key() + ":0"

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				assert.Equal(t, key, crdt.InitialTextNode().IDString())
			})
		}
		wg.Wait()
	})

	t.Run("concurrent read of a fresh node ID test", func(t *testing.T) {
		// DeepCopy of a split node shares its ID, so the first read of a cold
		// ID must be race-free: `go test -race` fails here if key fills its
		// cache with a plain write.
		actorID, _ := time.ActorIDFromHex("0123456789abcdef01234567")
		createdAt := time.NewTicket(7, 3, actorID)
		node := crdt.NewRGATreeSplitNode(
			crdt.NewRGATreeSplitNodeID(createdAt, 2),
			crdt.NewTextValue("abc", crdt.NewRHT()),
		)

		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				assert.Equal(t, "7:3:"+actorID.StringBase64()+":2", node.IDString())
			})
		}
		wg.Wait()
	})
}
