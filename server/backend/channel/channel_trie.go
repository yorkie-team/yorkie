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
	"github.com/yorkie-team/yorkie/api/types"
	pkgchannel "github.com/yorkie-team/yorkie/pkg/channel"
)

// ChannelTrie wraps PathTrie with Channel-specific methods.
type ChannelTrie struct {
	trie *PathTrie[*Channel]
}

// NewChannelTrie creates a new trie for storing channels.
func NewChannelTrie() *ChannelTrie {
	return &ChannelTrie{
		trie: NewPathTrie[*Channel](),
	}
}

// buildKeyPath constructs a key path with projectID prefix.
// Returns nil if the channel key is invalid.
func buildKeyPath(key types.ChannelRefKey) []string {
	keyPath, err := pkgchannel.ParseKeyPath(key.ChannelKey)
	if err != nil {
		return nil
	}
	return append([]string{key.ProjectID.String()}, keyPath...)
}

// Get retrieves a channel by its key.
func (ct *ChannelTrie) Get(key types.ChannelRefKey) *Channel {
	keyPath := buildKeyPath(key)
	if keyPath == nil {
		return nil
	}
	ch, _ := ct.trie.Get(keyPath)
	return ch
}

// GetOrInsert atomically retrieves or creates a channel.
func (ct *ChannelTrie) GetOrInsert(key types.ChannelRefKey, create func() *Channel) *Channel {
	keyPath := buildKeyPath(key)
	if keyPath == nil {
		return nil
	}
	return ct.trie.GetOrInsert(keyPath, create)
}

// Delete removes a channel by its key.
func (ct *ChannelTrie) Delete(key types.ChannelRefKey) {
	keyPath := buildKeyPath(key)
	if keyPath == nil {
		return
	}
	ct.trie.Delete(keyPath)
}

// ForEach traverses all channels in the trie (global).
func (ct *ChannelTrie) ForEach(fn func(*Channel) bool) {
	ct.trie.ForEach(fn)
}

// ForEachInProject traverses all channels in a specific project.
func (ct *ChannelTrie) ForEachInProject(projectID types.ID, fn func(*Channel) bool) {
	ct.trie.ForEachDescendant([]string{projectID.String()}, fn)
}

// ForEachDescendant traverses descendant channels under the given key path.
// The keyPath is relative to the project (without projectID prefix).
// keyPath must be non-empty.
func (ct *ChannelTrie) ForEachDescendant(key types.ChannelRefKey, fn func(*Channel) bool) {
	keyPath := buildKeyPath(key)
	if keyPath == nil {
		return
	}
	ct.trie.ForEachDescendant(keyPath, fn)
}

// ForEachPrefix traverses channels whose keys start with the given prefix.
// The prefix is scoped to the specified project, so only channels belonging to that project are visited.
func (ct *ChannelTrie) ForEachPrefix(prefix string, projectID types.ID, fn func(*Channel) bool) {
	// The fullPrefix includes projectID as the first segment, ensuring project isolation.
	fullPrefix := projectID.String() + "." + prefix
	ct.trie.ForEachPrefix(fullPrefix, fn)
}

// Len returns the total number of channels.
func (ct *ChannelTrie) Len() int {
	return ct.trie.Len()
}
