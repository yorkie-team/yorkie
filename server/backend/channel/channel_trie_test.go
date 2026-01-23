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

package channel

import (
	"fmt"
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

	t.Run("Len returns correct count", func(t *testing.T) {
		trie := NewChannelTrie()
		projectID := types.NewID()

		assert.Equal(t, 0, trie.Len())

		for i := 1; i <= 5; i++ {
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

		for i := 1; i <= 5; i++ {
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

		for i := 1; i <= 10; i++ {
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
		for i := 1; i <= 3; i++ {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID1,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			trie.GetOrInsert(refKey, func() *Channel {
				return createTestChannel(projectID1, key.Key(fmt.Sprintf("room-%d", i)))
			})
		}

		// Add channels to project 2
		for i := 1; i <= 5; i++ {
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
}

func TestChannelTrie_BuildKeyPath(t *testing.T) {
	t.Run("buildKeyPath creates correct path", func(t *testing.T) {
		projectID := types.NewID()

		// Simple key
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		keyPath := buildKeyPath(refKey)
		assert.Equal(t, []string{projectID.String(), "room-1"}, keyPath)

		// Hierarchical key
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1.desk-1"}
		keyPath2 := buildKeyPath(refKey2)
		assert.Equal(t, []string{projectID.String(), "room-1", "section-1", "desk-1"}, keyPath2)
	})

	t.Run("buildKeyPath returns nil for invalid key", func(t *testing.T) {
		projectID := types.NewID()

		// Empty key
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ""}
		keyPath := buildKeyPath(refKey)
		assert.Nil(t, keyPath)

		// Key starting with dot
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ".room-1"}
		keyPath2 := buildKeyPath(refKey2)
		assert.Nil(t, keyPath2)

		// Key ending with dot
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1."}
		keyPath3 := buildKeyPath(refKey3)
		assert.Nil(t, keyPath3)

		// Key with consecutive dots
		refKey4 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1..section-1"}
		keyPath4 := buildKeyPath(refKey4)
		assert.Nil(t, keyPath4)
	})
}
