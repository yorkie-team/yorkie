/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package channel

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func createTestChannel(projectID types.ID, channelKey key.Key) *Channel {
	return &Channel{
		Key: types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: channelKey,
		},
		Sessions: cmap.New[types.ID, *Session](),
	}
}

func TestChannelTrie_BasicOperations(t *testing.T) {
	t.Run("Get returns nil for non-existent channel", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		ch := trie.Get(refKey)

		assert.Nil(t, ch)
	})

	t.Run("Get returns nil for invalid channel key", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Invalid channel key (empty)
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ""}
		ch := trie.Get(refKey)

		assert.Nil(t, ch)
	})

	t.Run("Get returns channel after GetOrInsert", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		inserted := trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})

		retrieved := trie.Get(refKey)

		assert.NotNil(t, retrieved)
		assert.Same(t, inserted, retrieved)
	})

	t.Run("GetOrInsert creates channel when not exists", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		createCalled := false

		ch := trie.GetOrInsert(refKey, func() *Channel {
			createCalled = true
			return createTestChannel(projectID, "room-1")
		})

		assert.True(t, createCalled)
		assert.NotNil(t, ch)
		assert.Equal(t, refKey, ch.Key)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("GetOrInsert returns existing channel without calling create", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		// First insert
		ch1 := trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})

		// Second call should not create
		createCalled := false
		ch2 := trie.GetOrInsert(refKey, func() *Channel {
			createCalled = true
			return createTestChannel(projectID, "room-1")
		})

		assert.False(t, createCalled)
		assert.Same(t, ch1, ch2)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("GetOrInsert returns nil for invalid channel key", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Invalid channel key (starts with dot)
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ".room-1"}
		createCalled := false

		ch := trie.GetOrInsert(refKey, func() *Channel {
			createCalled = true
			return createTestChannel(projectID, ".room-1")
		})

		assert.False(t, createCalled)
		assert.Nil(t, ch)
	})

	t.Run("Delete removes channel", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})
		assert.Equal(t, 1, trie.Len())

		trie.Delete(refKey)

		assert.Nil(t, trie.Get(refKey))
		assert.Equal(t, 0, trie.Len())
	})

	t.Run("Delete with invalid key does not panic", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Should not panic
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ""}
		trie.Delete(refKey)

		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ".invalid"}
		trie.Delete(refKey2)
	})

	t.Run("Delete non-existent channel with valid key does nothing", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Add one channel
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		trie.GetOrInsert(refKey1, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})
		assert.Equal(t, 1, trie.Len())

		// Delete non-existent channel (valid key)
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		trie.Delete(refKey2)

		// Original channel should still exist
		assert.Equal(t, 1, trie.Len())
		assert.NotNil(t, trie.Get(refKey1))
	})

	t.Run("Len returns correct count", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		assert.Equal(t, 0, trie.Len())

		for i := range 5 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		assert.Equal(t, 5, trie.Len())
	})
}

func TestChannelTrie_ForEach(t *testing.T) {
	t.Run("ForEach iterates all channels", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		for i := range 5 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		count := 0
		trie.ForEach(func(ch *Channel) bool {
			count++
			assert.NotNil(t, ch)
			return true
		})

		assert.Equal(t, 5, count)
	})

	t.Run("ForEach on empty trie", func(t *testing.T) {
		trie := NewChannelTrie()

		count := 0
		trie.ForEach(func(ch *Channel) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})

	t.Run("ForEach with early termination", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		for i := range 10 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		count := 0
		trie.ForEach(func(ch *Channel) bool {
			count++
			return count < 5
		})

		assert.Equal(t, 5, count)
	})
}

func TestChannelTrie_ForEachInProject(t *testing.T) {
	t.Run("ForEachInProject iterates only project channels", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID1 := types.NewID()
		projectID2 := types.NewID()

		// Add channels to project 1
		for i := range 3 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID1,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID1, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		// Add channels to project 2
		for i := range 5 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID2,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID2, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		// Count project 1 channels
		count1 := 0
		trie.ForEachInProject(projectID1, func(ch *Channel) bool {
			count1++
			assert.Equal(t, projectID1, ch.Key.ProjectID)
			return true
		})
		assert.Equal(t, 3, count1)

		// Count project 2 channels
		count2 := 0
		trie.ForEachInProject(projectID2, func(ch *Channel) bool {
			count2++
			assert.Equal(t, projectID2, ch.Key.ProjectID)
			return true
		})
		assert.Equal(t, 5, count2)
	})

	t.Run("ForEachInProject on non-existent project", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()
		nonExistentProjectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})

		count := 0
		trie.ForEachInProject(nonExistentProjectID, func(ch *Channel) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})

	t.Run("ForEachInProject with early termination", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		for i := range 10 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		count := 0
		trie.ForEachInProject(projectID, func(ch *Channel) bool {
			count++
			return count < 5
		})

		assert.Equal(t, 5, count)
	})
}

func TestChannelTrie_ForEachDescendant(t *testing.T) {
	t.Run("ForEachDescendant traverses hierarchical channels", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Create hierarchical channels
		keys := []key.Key{
			"room-1",
			"room-1.section-1",
			"room-1.section-1.desk-1",
			"room-1.section-1.desk-2",
			"room-1.section-2",
			"room-2", // Different branch
		}

		for _, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}

		// Count descendants of room-1
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		count := 0
		trie.ForEachDescendant(refKey, func(ch *Channel) bool {
			count++
			return true
		})
		assert.Equal(t, 5, count) // room-1 + section-1 + desk-1 + desk-2 + section-2

		// Count descendants of room-1.section-1
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1"}
		count2 := 0
		trie.ForEachDescendant(refKey2, func(ch *Channel) bool {
			count2++
			return true
		})
		assert.Equal(t, 3, count2) // section-1 + desk-1 + desk-2
	})

	t.Run("ForEachDescendant with invalid key does nothing", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ""}
		count := 0
		trie.ForEachDescendant(refKey, func(ch *Channel) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})

	t.Run("ForEachDescendant on non-existent path in existing shard", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Create channels under room-1 shard
		keys := []key.Key{
			"room-1",
			"room-1.section-1",
			"room-1.section-2",
		}
		for _, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}

		// Try to traverse non-existent path in existing shard
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.nonexistent"}
		count := 0
		trie.ForEachDescendant(refKey, func(ch *Channel) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})

	t.Run("ForEachDescendant on non-existent shard", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Create a channel in a different shard
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		trie.GetOrInsert(refKey1, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})

		// Try to traverse in non-existent shard
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2.section-1"}
		count := 0
		trie.ForEachDescendant(refKey, func(ch *Channel) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})

	t.Run("ForEachDescendant with early termination", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Create hierarchical channels
		keys := []key.Key{
			"room-1",
			"room-1.section-1",
			"room-1.section-2",
			"room-1.section-3",
			"room-1.section-4",
			"room-1.section-5",
		}

		for _, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		count := 0
		trie.ForEachDescendant(refKey, func(ch *Channel) bool {
			count++
			return count < 3
		})

		assert.Equal(t, 3, count)
	})
}

func TestChannelTrie_ForEachPrefix(t *testing.T) {
	t.Run("ForEachPrefix filters by prefix", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		keys := []key.Key{"room-1", "room-10", "room-2", "lobby", "lobby-vip"}
		for _, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}

		// Find channels starting with "room-1"
		count := 0
		trie.ForEachPrefix("room-1", projectID, func(ch *Channel) bool {
			count++
			return true
		})
		assert.Equal(t, 2, count) // room-1, room-10

		// Find channels starting with "lobby"
		count2 := 0
		trie.ForEachPrefix("lobby", projectID, func(ch *Channel) bool {
			count2++
			return true
		})
		assert.Equal(t, 2, count2) // lobby, lobby-vip
	})

	t.Run("ForEachPrefix with empty prefix returns all channels in project", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		keys := []key.Key{"room-1", "room-2", "lobby"}
		for _, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}

		// Empty prefix should match all channels in project
		count := 0
		trie.ForEachPrefix("", projectID, func(ch *Channel) bool {
			count++
			assert.Equal(t, projectID, ch.Key.ProjectID)
			return true
		})

		assert.Equal(t, 3, count)
	})

	t.Run("ForEachPrefix respects project isolation", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID1 := types.NewID()
		projectID2 := types.NewID()

		// Same channel key in different projects
		refKey1 := types.ChannelRefKey{ProjectID: projectID1, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID2, ChannelKey: "room-1"}

		trie.GetOrInsert(refKey1, func() *Channel {
			return createTestChannel(projectID1, "room-1")
		})
		trie.GetOrInsert(refKey2, func() *Channel {
			return createTestChannel(projectID2, "room-1")
		})

		// Search in project 1
		count1 := 0
		trie.ForEachPrefix("room", projectID1, func(ch *Channel) bool {
			count1++
			assert.Equal(t, projectID1, ch.Key.ProjectID)
			return true
		})
		assert.Equal(t, 1, count1)

		// Search in project 2
		count2 := 0
		trie.ForEachPrefix("room", projectID2, func(ch *Channel) bool {
			count2++
			assert.Equal(t, projectID2, ch.Key.ProjectID)
			return true
		})
		assert.Equal(t, 1, count2)
	})

	t.Run("ForEachPrefix with non-matching prefix returns nothing", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, "room-1")
		})

		count := 0
		trie.ForEachPrefix("lobby", projectID, func(ch *Channel) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})

	t.Run("ForEachPrefix with early termination", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		for i := range 10 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		count := 0
		trie.ForEachPrefix("room", projectID, func(ch *Channel) bool {
			count++
			return count < 5
		})

		assert.Equal(t, 5, count)
	})

	t.Run("ForEachPrefix with hierarchical keys", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Create hierarchical channels
		keys := []key.Key{
			"room-1",
			"room-1.section-1",
			"room-1.section-2",
			"room-10",
			"room-2",
		}

		for _, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}

		// Find channels starting with "room-1"
		var foundKeys []key.Key
		trie.ForEachPrefix("room-1", projectID, func(ch *Channel) bool {
			foundKeys = append(foundKeys, ch.Key.ChannelKey)
			return true
		})

		assert.Len(t, foundKeys, 4) // room-1, room-1.section-1, room-1.section-2, room-10
		assert.ElementsMatch(t, []key.Key{"room-1", "room-1.section-1", "room-1.section-2", "room-10"}, foundKeys)
	})
}

func TestChannelTrie_ConcurrentOperations(t *testing.T) {
	t.Run("concurrent GetOrInsert on same key calls create once", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		var createCount int32 = 0
		var wg sync.WaitGroup

		for range 100 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				trie.GetOrInsert(refKey, func() *Channel {
					atomic.AddInt32(&createCount, 1)
					return createTestChannel(projectID, "room-1")
				})
			}()
		}

		wg.Wait()

		assert.Equal(t, int32(1), createCount)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("concurrent reads and writes", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Pre-populate
		for i := range 10 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		var wg sync.WaitGroup
		const numReaders = 50
		const numWriters = 10

		// Writers
		for w := range numWriters {
			wg.Add(1)
			go func(wid int) {
				defer wg.Done()
				for i := range 10 {
					refKey := types.ChannelRefKey{
						ProjectID:  projectID,
						ChannelKey: key.Key(fmt.Sprintf("room-%d.section-%d", wid, i)),
					}
					trie.GetOrInsert(refKey, func() *Channel {
						return createTestChannel(projectID, refKey.ChannelKey)
					})
				}
			}(w)
		}

		// Readers
		for range numReaders {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 10 {
					trie.ForEach(func(ch *Channel) bool {
						_ = ch.Key.ChannelKey
						return true
					})
				}
			}()
		}

		wg.Wait()

		// Verify trie is in a consistent state after concurrent operations
		// Initial: 10 channels (room-0 to room-9)
		// Writers added: 10 writers * 10 channels = 100 (room-{wid}.section-{0-9})
		// Total: 10 + 100 = 110
		assert.Equal(t, 110, trie.Len())

		// Verify all initial channels exist
		for i := range 10 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			ch := trie.Get(refKey)
			assert.NotNil(t, ch, "missing initial channel room-%d", i)
		}

		// Verify all written channels exist
		for wid := range numWriters {
			for i := range 10 {
				refKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d.section-%d", wid, i)),
				}
				ch := trie.Get(refKey)
				assert.NotNil(t, ch, "missing channel room-%d.section-%d", wid, i)
			}
		}
	})

	t.Run("concurrent deletes", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		// Insert channels
		for i := range 100 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		var wg sync.WaitGroup

		// Concurrent deletes
		for i := range 100 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				refKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d", idx)),
				}
				trie.Delete(refKey)
			}(i)
		}

		wg.Wait()

		assert.Equal(t, 0, trie.Len())
	})
}

func TestChannelTrie_ConcurrentForEachInProjectWithModifications(t *testing.T) {
	trie := NewChannelTrie()
	projectID := types.NewID()

	// Pre-populate
	for i := range 50 {
		refKey := types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
		}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, refKey.ChannelKey)
		})
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	// Readers doing ForEachInProject
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				count := 0
				trie.ForEachInProject(projectID, func(ch *Channel) bool {
					count++
					return true
				})
				_ = count
			}
		}()
	}

	// Writers adding channels
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 20 {
				refKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("new-room-%d-%d", wid, j)),
				}
				trie.GetOrInsert(refKey, func() *Channel {
					return createTestChannel(projectID, refKey.ChannelKey)
				})
			}
		}(i)
	}

	// Deleters removing channels
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			for j := range 5 {
				refKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d", did*5+j)),
				}
				trie.Delete(refKey)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// Initial: 50 channels (room-0 to room-49)
	// Added: 10 writers * 20 = 200 channels (new-room-{wid}-{j})
	// Deleted: 10 deleters * 5 = 50 channels (room-0 to room-49)
	// Expected: 50 - 50 + 200 = 200 channels
	finalCount := 0
	trie.ForEachInProject(projectID, func(ch *Channel) bool {
		finalCount++
		return true
	})
	assert.Equal(t, 200, finalCount)

	// Verify all added channels exist
	for wid := range numWriters {
		for j := range 20 {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("new-room-%d-%d", wid, j)),
			}
			ch := trie.Get(refKey)
			assert.NotNil(t, ch, "missing new-room-%d-%d", wid, j)
		}
	}
}

func TestChannelTrie_ConcurrentForEachDescendantWithModifications(t *testing.T) {
	trie := NewChannelTrie()
	projectID := types.NewID()

	// Pre-populate with hierarchical structure
	parentKeys := []key.Key{"room-1", "room-1.section-1", "room-1.section-2"}
	for _, k := range parentKeys {
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, k)
		})
	}

	for i := range 20 {
		k := key.Key(fmt.Sprintf("room-1.section-1.desk-%d", i))
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, k)
		})
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	parentRefKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

	// Readers doing ForEachDescendant
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				count := 0
				trie.ForEachDescendant(parentRefKey, func(ch *Channel) bool {
					count++
					return true
				})
				_ = count
			}
		}()
	}

	// Writers adding descendants
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 10 {
				k := key.Key(fmt.Sprintf("room-1.section-1.new-desk-%d-%d", wid, j))
				refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
				trie.GetOrInsert(refKey, func() *Channel {
					return createTestChannel(projectID, k)
				})
			}
		}(i)
	}

	// Deleters removing descendants
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			for j := range 2 {
				k := key.Key(fmt.Sprintf("room-1.section-1.desk-%d", did*2+j))
				refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
				trie.Delete(refKey)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// Initial: 3 (room-1, section-1, section-2) + 20 (desk-0 to desk-19) = 23 channels
	// Added: 10 writers * 10 = 100 channels (new-desk-{wid}-{j})
	// Deleted: 10 deleters * 2 = 20 channels (desk-0 to desk-19)
	// Expected: 23 - 20 + 100 = 103 channels
	finalCount := 0
	trie.ForEachDescendant(parentRefKey, func(ch *Channel) bool {
		finalCount++
		return true
	})
	assert.Equal(t, 103, finalCount)

	// Verify parent channels still exist
	for _, k := range []key.Key{"room-1", "room-1.section-1", "room-1.section-2"} {
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
		ch := trie.Get(refKey)
		assert.NotNil(t, ch, "missing parent channel %s", k)
	}

	// Verify all added channels exist
	for wid := range numWriters {
		for j := range 10 {
			k := key.Key(fmt.Sprintf("room-1.section-1.new-desk-%d-%d", wid, j))
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			ch := trie.Get(refKey)
			assert.NotNil(t, ch, "missing new-desk-%d-%d", wid, j)
		}
	}
}

func TestChannelTrie_ConcurrentForEachPrefixWithModifications(t *testing.T) {
	trie := NewChannelTrie()
	projectID := types.NewID()

	// Pre-populate with channels matching prefix
	for i := range 30 {
		k := key.Key(fmt.Sprintf("room-%d", i))
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
		trie.GetOrInsert(refKey, func() *Channel {
			return createTestChannel(projectID, k)
		})
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	// Readers doing ForEachPrefix
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				count := 0
				trie.ForEachPrefix("room-1", projectID, func(ch *Channel) bool {
					count++
					return true
				})
				_ = count
			}
		}()
	}

	// Writers adding matching channels
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 10 {
				k := key.Key(fmt.Sprintf("room-1%d%d", wid, j)) // Matches "room-1" prefix
				refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
				trie.GetOrInsert(refKey, func() *Channel {
					return createTestChannel(projectID, k)
				})
			}
		}(i)
	}

	// Deleters removing matching channels
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			k := key.Key(fmt.Sprintf("room-%d", 10+did)) // room-10 through room-19
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.Delete(refKey)
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// Initial matching "room-1" prefix: room-1, room-10 to room-19 = 11 channels
	// Added matching "room-1" prefix: 10 writers * 10 = 100 channels (room-1{wid}{j})
	// Deleted matching "room-1" prefix: room-10 to room-19 = 10 channels
	// Expected: 11 - 10 + 100 = 101 channels
	finalCount := 0
	trie.ForEachPrefix("room-1", projectID, func(ch *Channel) bool {
		finalCount++
		return true
	})
	assert.Equal(t, 101, finalCount)

	// Verify room-1 still exists
	refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
	ch := trie.Get(refKey)
	assert.NotNil(t, ch, "missing room-1")

	// Verify all added channels exist
	for wid := range numWriters {
		for j := range 10 {
			k := key.Key(fmt.Sprintf("room-1%d%d", wid, j))
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			ch := trie.Get(refKey)
			assert.NotNil(t, ch, "missing room-1%d%d", wid, j)
		}
	}
}

func TestChannelTrie_ConcurrentHierarchicalChannelCreation(t *testing.T) {
	trie := NewChannelTrie()
	projectID := types.NewID()

	var wg sync.WaitGroup
	const numGoroutines = 50

	// Concurrent creation of parent and child channels
	for i := range numGoroutines {
		wg.Add(3)

		// Create parent channel
		go func(idx int) {
			defer wg.Done()
			k := key.Key(fmt.Sprintf("room-%d", idx%10))
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}(i)

		// Create child channel
		go func(idx int) {
			defer wg.Done()
			k := key.Key(fmt.Sprintf("room-%d.section-%d", idx%10, idx%5))
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}(i)

		// Create grandchild channel
		go func(idx int) {
			defer wg.Done()
			k := key.Key(fmt.Sprintf("room-%d.section-%d.desk-%d", idx%10, idx%5, idx%3))
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID, k)
			})
		}(i)
	}

	wg.Wait()

	// Verify hierarchical structure is consistent
	// Analysis: With idx in 0..49, and patterns idx%10, idx%5, idx%3:
	// - Parents: 10 unique (room-0 to room-9)
	// - Children: 10 unique (room-{a}.section-{a%5} for a=0..9)
	// - Grandchildren: 30 unique (each of 10 children gets 3 desks)
	// Total: 10 + 10 + 30 = 50 unique channels
	totalCount := trie.Len()
	assert.Equal(t, 50, totalCount)

	// Verify ForEach count matches Len
	forEachCount := 0
	trie.ForEach(func(ch *Channel) bool {
		forEachCount++
		return true
	})
	assert.Equal(t, totalCount, forEachCount)

	// Verify all parent channels exist
	for i := range 10 {
		refKey := types.ChannelRefKey{
			ProjectID:  projectID,
			ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
		}
		ch := trie.Get(refKey)
		assert.NotNil(t, ch, "missing room-%d", i)
	}
}

func TestChannelTrie_ConcurrentMultiProjectOperations(t *testing.T) {
	trie := NewChannelTrie()
	projectIDs := make([]types.ID, 5)
	for i := range projectIDs {
		projectIDs[i] = types.NewID()
	}

	var wg sync.WaitGroup
	const numGoroutines = 30

	// Concurrent operations across multiple projects
	for i := range numGoroutines {
		wg.Add(3)

		// Insert to random projects
		go func(idx int) {
			defer wg.Done()
			projectID := projectIDs[idx%len(projectIDs)]
			for j := range 10 {
				k := key.Key(fmt.Sprintf("room-%d-%d", idx, j))
				refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
				trie.GetOrInsert(refKey, func() *Channel {
					return createTestChannel(projectID, k)
				})
			}
		}(i)

		// ForEachInProject on random projects
		go func(idx int) {
			defer wg.Done()
			projectID := projectIDs[idx%len(projectIDs)]
			count := 0
			trie.ForEachInProject(projectID, func(ch *Channel) bool {
				count++
				return true
			})
			_ = count
		}(i)

		// Delete from random projects
		go func(idx int) {
			defer wg.Done()
			projectID := projectIDs[idx%len(projectIDs)]
			k := key.Key(fmt.Sprintf("room-%d-0", idx))
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			trie.Delete(refKey)
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// 30 goroutines, each inserts 10 channels and deletes 1
	// Total inserted: 30 * 10 = 300 channels
	// Total deleted: 0-30 (depends on race timing - delete may run before insert)
	// Expected range: 270 (all deletes successful) to 300 (no deletes successful)
	totalCount := trie.Len()
	assert.GreaterOrEqual(t, totalCount, 270, "total should be at least 270 (300 inserted - 30 deleted)")
	assert.LessOrEqual(t, totalCount, 300, "total should be at most 300 (all inserted)")

	// Verify each project has consistent state and correct project ID
	totalFromProjects := 0
	for _, projectID := range projectIDs {
		count := 0
		trie.ForEachInProject(projectID, func(ch *Channel) bool {
			assert.Equal(t, projectID, ch.Key.ProjectID)
			count++
			return true
		})
		// Each project should have 54-60 channels
		// (6 goroutines * 10 inserted, 0-6 deleted depending on race)
		assert.GreaterOrEqual(t, count, 54, "project should have at least 54 channels")
		assert.LessOrEqual(t, count, 60, "project should have at most 60 channels")
		totalFromProjects += count
	}

	// Verify total from projects matches trie length
	assert.Equal(t, totalCount, totalFromProjects)
}

func TestChannelTrie_BuildShardKeyAndRemainingPath(t *testing.T) {
	t.Run("buildShardKey creates correct shard key", func(t *testing.T) {
		projectID := types.NewID()

		// Simple key
		shardKey := buildShardKey(projectID, "room-1")
		assert.Equal(t, projectID.String()+".room-1", shardKey)

		// Hierarchical key - shard key only includes first level
		shardKey2 := buildShardKey(projectID, "room-1.section-1.desk-1")
		assert.Equal(t, projectID.String()+".room-1", shardKey2)
	})

	t.Run("buildRemainingPath creates correct remaining path", func(t *testing.T) {
		// Simple key - no remaining path
		remaining := buildRemainingPath("room-1")
		assert.Nil(t, remaining)

		// Hierarchical key - remaining path after first level
		remaining2 := buildRemainingPath("room-1.section-1.desk-1")
		assert.Equal(t, []string{"section-1", "desk-1"}, remaining2)
	})

	t.Run("buildShardKey returns empty for invalid key", func(t *testing.T) {
		projectID := types.NewID()

		// Empty key
		shardKey := buildShardKey(projectID, "")
		assert.Equal(t, "", shardKey)

		// Key starting with dot
		shardKey2 := buildShardKey(projectID, ".room-1")
		assert.Equal(t, "", shardKey2)

		// Key ending with dot
		shardKey3 := buildShardKey(projectID, "room-1.")
		assert.Equal(t, "", shardKey3)

		// Key with consecutive dots
		shardKey4 := buildShardKey(projectID, "room-1..section-1")
		assert.Equal(t, "", shardKey4)
	})
}
