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

package database

import (
	"fmt"
	"sync"

	"github.com/google/btree"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
)

var (
	// ErrInvalidPresenceSeq is returned when the presence sequence is invalid.
	ErrInvalidPresenceSeq = errors.Internal("invalid presence sequence").WithCode("ErrInvalidPresenceSeq")
)

// PresenceStorage is an in-memory database for presence changes.
// It serves as the Source of Truth (SOT) for all presence data.
type PresenceStorage struct {
	mu   sync.RWMutex
	tree *btree.BTreeG[*PresenceChangeInfo]
}

// NewPresenceStorage creates a new instance of PresenceStorage.
func NewPresenceStorage() *PresenceStorage {
	return &PresenceStorage{
		tree: btree.NewG(32, func(a, b *PresenceChangeInfo) bool {
			// Primary sort: ProjectID
			if a.ProjectID != b.ProjectID {
				return a.ProjectID < b.ProjectID
			}
			// Secondary sort: DocID
			if a.DocID != b.DocID {
				return a.DocID < b.DocID
			}
			// Tertiary sort: PrSeq
			return a.PrSeq < b.PrSeq
		}),
	}
}

// StorePresences stores multiple presence changes in batch.
func (s *PresenceStorage) StorePresences(presences []*PresenceChangeInfo) error {
	if len(presences) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, presence := range presences {
		if presence == nil {
			continue
		}
		if presence.PrSeq <= 0 {
			return fmt.Errorf("presence sequence must be positive: %w", ErrInvalidPresenceSeq)
		}

		s.tree.ReplaceOrInsert(presence.DeepCopy())
	}

	return nil
}

// GetPresencesInRange retrieves all presence changes within the specified PrSeq range for a specific document.
func (s *PresenceStorage) GetPresencesInRange(
	docRefKey types.DocRefKey,
	from, to int64,
) ([]*PresenceChangeInfo, error) {
	if from > to {
		return nil, fmt.Errorf("from (%d) > to (%d): %w", from, to, ErrInvalidPresenceSeq)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.tree.Len() == 0 {
		return []*PresenceChangeInfo{}, nil
	}

	var result []*PresenceChangeInfo
	searchKey := &PresenceChangeInfo{
		ProjectID: docRefKey.ProjectID,
		DocID:     docRefKey.DocID,
		PrSeq:     from,
	}

	s.tree.AscendGreaterOrEqual(searchKey, func(item *PresenceChangeInfo) bool {
		// Stop if we've moved to a different document
		if item.ProjectID != docRefKey.ProjectID || item.DocID != docRefKey.DocID {
			return false
		}

		// Stop if we've passed the end of our target range
		if item.PrSeq > to {
			return false
		}

		result = append(result, item.DeepCopy())
		return true // Continue iteration
	})

	return result, nil
}

// DeletePresencesByActor removes all presence changes created by a specific actor.
func (s *PresenceStorage) DeletePresencesByActor(actorID string) int {
	if actorID == "" {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var toDelete []*PresenceChangeInfo

	// Find all presences for the actor
	s.tree.Ascend(func(item *PresenceChangeInfo) bool {
		if item.ActorID == types.ID(actorID) {
			toDelete = append(toDelete, item)
		}
		return true // Continue iteration
	})

	// Delete collected items
	deletedCount := 0
	for _, item := range toDelete {
		if _, ok := s.tree.Delete(item); ok {
			deletedCount++
		}
	}

	return deletedCount
}
