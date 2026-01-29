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
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/pkg/trie"
)

// ChannelTrie wraps ShardedPathTrie with Channel-specific methods.
// It implements channel domain sharding strategy: projectID + separator + firstLevelKey.
type ChannelTrie struct {
	trie *trie.ShardedPathTrie[*Channel]
}

// NewChannelTrie creates a new trie for storing channels.
func NewChannelTrie() *ChannelTrie {
	return &ChannelTrie{
		trie: trie.NewShardedPathTrie[*Channel](),
	}
}

// buildShardKey constructs a shard key from projectID and channelKey.
// Sharding strategy: projectID + ChannelKeyPathSeparator + firstLevelKey
// Example: "projectID.room-1" for channel key "room-1.section-1"
func buildShardKey(projectID types.ID, channelKey key.Key) string {
	keyPath, err := channel.ParseKeyPath(channelKey)
	if err != nil || len(keyPath) == 0 {
		return ""
	}
	return projectID.String() + channel.ChannelKeyPathSeparator + keyPath[0]
}

// buildRemainingPath returns the path after the first level key.
// Example: ["section-1"] for channel key "room-1.section-1"
func buildRemainingPath(channelKey key.Key) []string {
	keyPath, err := channel.ParseKeyPath(channelKey)
	if err != nil || len(keyPath) <= 1 {
		return nil
	}
	return keyPath[1:]
}

// Get retrieves a channel by its key.
func (ct *ChannelTrie) Get(refKey types.ChannelRefKey) *Channel {
	shardKey := buildShardKey(refKey.ProjectID, refKey.ChannelKey)
	if shardKey == "" {
		return nil
	}
	remaining := buildRemainingPath(refKey.ChannelKey)
	ch, _ := ct.trie.Get(shardKey, remaining)
	return ch
}

// GetOrInsert atomically retrieves or creates a channel.
func (ct *ChannelTrie) GetOrInsert(refKey types.ChannelRefKey, create func() *Channel) *Channel {
	shardKey := buildShardKey(refKey.ProjectID, refKey.ChannelKey)
	if shardKey == "" {
		return nil
	}
	remaining := buildRemainingPath(refKey.ChannelKey)
	return ct.trie.GetOrInsert(shardKey, remaining, create)
}

// Delete removes a channel by its key and cleans up empty shard.
// The caller should hold appropriate locks to prevent race conditions.
func (ct *ChannelTrie) Delete(refKey types.ChannelRefKey) {
	shardKey := buildShardKey(refKey.ProjectID, refKey.ChannelKey)
	if shardKey == "" {
		return
	}
	remaining := buildRemainingPath(refKey.ChannelKey)
	ct.trie.Delete(shardKey, remaining)
	ct.trie.DeleteShardIfEmpty(shardKey)
}

// ForEach traverses all channels in the trie (global).
func (ct *ChannelTrie) ForEach(fn func(*Channel) bool) {
	ct.trie.ForEach(fn)
}

// ForEachInProject traverses all channels in a specific project.
func (ct *ChannelTrie) ForEachInProject(projectID types.ID, fn func(*Channel) bool) {
	shardPrefix := projectID.String() + channel.ChannelKeyPathSeparator
	ct.trie.ForEachByShard(shardPrefix, fn)
}

// ForEachDescendant traverses descendant channels under the given key path.
func (ct *ChannelTrie) ForEachDescendant(refKey types.ChannelRefKey, fn func(*Channel) bool) {
	shardKey := buildShardKey(refKey.ProjectID, refKey.ChannelKey)
	if shardKey == "" {
		return
	}
	remaining := buildRemainingPath(refKey.ChannelKey)
	ct.trie.ForEachDescendant(shardKey, remaining, fn)
}

// ForEachPrefix traverses channels whose keys start with the given prefix.
// The prefix is scoped to the specified project, so only channels belonging to that project are visited.
// This method handles channel-specific separator logic.
func (ct *ChannelTrie) ForEachPrefix(prefix string, projectID types.ID, fn func(*Channel) bool) {
	if prefix == "" {
		ct.ForEachInProject(projectID, fn)
		return
	}

	// Parse prefix using channel domain separator
	parts := strings.SplitN(prefix, channel.ChannelKeyPathSeparator, 2)
	firstLevelKey := parts[0]
	shardKeyPrefix := projectID.String() + channel.ChannelKeyPathSeparator + firstLevelKey

	// Find matching shards and filter by prefix
	for _, shardKey := range ct.trie.ShardKeys() {
		if !strings.HasPrefix(shardKey, shardKeyPrefix) {
			continue
		}

		shouldContinue := true
		ct.trie.ForEachInShard(shardKey, func(ch *Channel) bool {
			if strings.HasPrefix(string(ch.Key.ChannelKey), prefix) {
				shouldContinue = fn(ch)
				return shouldContinue
			}
			return true
		})

		if !shouldContinue {
			return
		}
	}
}

// Len returns the total number of channels.
func (ct *ChannelTrie) Len() int {
	return ct.trie.Len()
}
