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

package mongo

import (
	"sync"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// ProjectCache is a cache for project information with multiple access paths.
// It uses a single LRU cache with ID as the primary key and maintains a
// secondary index for API key lookups to minimize memory usage and ensure
// cache consistency.
type ProjectCache struct {
	// Primary cache: ID is the primary key
	cache *cache.LRUWithExpires[types.ID, *database.ProjectInfo]

	// Secondary index: API key -> ID mapping
	apiKeyToID sync.Map // map[string]types.ID
}

// NewProjectCache creates a new project cache with the given size and TTL.
func NewProjectCache(size int, ttl time.Duration) (*ProjectCache, error) {
	pc := &ProjectCache{
		apiKeyToID: sync.Map{},
	}

	// Create cache with eviction callback to clean up secondary index
	onEvict := func(id types.ID, info *database.ProjectInfo) {
		// When an entry is evicted from primary cache (by TTL or LRU),
		// remove it from the secondary index as well
		pc.apiKeyToID.Delete(info.PublicKey)
	}

	c, err := cache.NewLRUWithExpires[types.ID, *database.ProjectInfo](
		size,
		ttl,
		"project",
		onEvict,
	)
	if err != nil {
		return nil, err
	}

	pc.cache = c
	return pc, nil
}

// Name returns the cache name.
func (pc *ProjectCache) Name() string {
	return pc.cache.Name()
}

// Stats returns the cache statistics.
func (pc *ProjectCache) Stats() *cache.Stats {
	return pc.cache.Stats()
}

// Len returns the number of items in the cache.
func (pc *ProjectCache) Len() int {
	return pc.cache.Len()
}

// GetByAPIKey retrieves a project by API key.
func (pc *ProjectCache) GetByAPIKey(apiKey string) (*database.ProjectInfo, bool) {
	// Look up ID from secondary index
	idVal, ok := pc.apiKeyToID.Load(apiKey)
	if !ok {
		return nil, false
	}

	id := idVal.(types.ID)

	// Get from primary cache
	info, found := pc.cache.Get(id)
	if !found {
		// Primary cache entry was evicted but secondary index still exists
		// Clean up the stale secondary index entry
		pc.apiKeyToID.Delete(apiKey)
		return nil, false
	}

	return info, true
}

// GetByID retrieves a project by ID.
func (pc *ProjectCache) GetByID(id types.ID) (*database.ProjectInfo, bool) {
	return pc.cache.Get(id)
}

// Add adds a project to the cache.
func (pc *ProjectCache) Add(info *database.ProjectInfo) {
	project := info.DeepCopy()

	// Add to primary cache
	pc.cache.Add(info.ID, project)

	// Update secondary index
	pc.apiKeyToID.Store(info.PublicKey, info.ID)
}

// Remove removes a project from the cache using both API key and ID.
func (pc *ProjectCache) Remove(id types.ID) {
	if info, ok := pc.cache.Peek(id); ok {
		pc.apiKeyToID.Delete(info.PublicKey)
	}
	pc.cache.Remove(id)
}

// Purge removes all entries from the cache.
func (pc *ProjectCache) Purge() {
	pc.cache.Purge()
	pc.apiKeyToID.Range(func(key, _ any) bool {
		pc.apiKeyToID.Delete(key)
		return true
	})
}
