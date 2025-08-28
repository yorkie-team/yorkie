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
	"sort"
	"sync"

	"github.com/google/btree"

	"github.com/yorkie-team/yorkie/api/types"
)

var (
	// ErrInvalidPresenceSeq is returned when the presence sequence is invalid.
	ErrInvalidPresenceSeq = fmt.Errorf("invalid presence sequence")
)

// PresenceRange represents a range of presence sequences from start to end
type PresenceRange struct {
	From int64 // Starting presence sequence
	To   int64 // Ending presence sequence
}

// PresenceStorage manages presence changes using B-tree for efficient range queries.
// This is an in-memory storage optimized for presence data that needs fast from-to queries.
type PresenceStorage struct {
	mu   sync.RWMutex
	tree *btree.BTreeG[*PresenceChangeInfo]
}

// NewPresenceStorage creates a new instance of PresenceStorage.
func NewPresenceStorage() *PresenceStorage {
	return &PresenceStorage{
		tree: btree.NewG(32, func(a, b *PresenceChangeInfo) bool {
			// Primary sort: PrSeq
			if a.PrSeq != b.PrSeq {
				return a.PrSeq < b.PrSeq
			}
			// Secondary sort: ProjectID, DocID for completeness
			if a.ProjectID != b.ProjectID {
				return a.ProjectID < b.ProjectID
			}
			return a.DocID < b.DocID
		}),
	}
}

// StorePresence stores a presence info in the storage.
func (s *PresenceStorage) StorePresence(presence *PresenceChangeInfo) error {
	if presence == nil {
		return fmt.Errorf("presence cannot be nil")
	}

	if presence.PrSeq <= 0 {
		return fmt.Errorf("presence sequence must be positive: %w", ErrInvalidPresenceSeq)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tree.ReplaceOrInsert(presence.DeepCopy())
	return nil
}

// StorePresences stores multiple presence infos in batch.
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

// GetPresencesInRange retrieves all presence infos within the specified range
// for a specific document.
func (s *PresenceStorage) GetPresencesInRange(
	docRefKey types.DocRefKey,
	from, to int64,
) ([]*PresenceChangeInfo, error) {
	if from > to {
		return nil, fmt.Errorf("from (%d) > to (%d): %w", from, to, ErrInvalidPresenceSeq)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return empty result if the tree is empty
	if s.tree.Len() == 0 {
		return nil, nil
	}

	var result []*PresenceChangeInfo

	// Create a dummy presence with the minimum sequence as search key
	searchKey := &PresenceChangeInfo{
		ProjectID: docRefKey.ProjectID,
		DocID:     docRefKey.DocID,
		PrSeq:     from,
	}

	// Define a callback function to process each presence in the range
	s.tree.AscendGreaterOrEqual(searchKey, func(item *PresenceChangeInfo) bool {
		presence := item

		// Stop iteration when we've passed the end of our target range
		if presence.PrSeq > to {
			return false
		}

		// Skip if not for the same document
		if presence.ProjectID != docRefKey.ProjectID || presence.DocID != docRefKey.DocID {
			// Since our tree is sorted by PrSeq first, we need to continue
			// until we find the right document or pass the range
			return presence.PrSeq <= to
		}

		// Add this presence to the result
		result = append(result, presence.DeepCopy())

		return true
	})

	return result, nil
}

// GetAllPresencesInRange retrieves all presence infos within the specified range
// regardless of document (useful for debugging/monitoring).
func (s *PresenceStorage) GetAllPresencesInRange(from, to int64) ([]*PresenceChangeInfo, error) {
	if from > to {
		return nil, fmt.Errorf("from (%d) > to (%d): %w", from, to, ErrInvalidPresenceSeq)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return empty result if the tree is empty
	if s.tree.Len() == 0 {
		return nil, nil
	}

	var result []*PresenceChangeInfo

	// Create a dummy presence with the minimum sequence as search key
	searchKey := &PresenceChangeInfo{PrSeq: from}

	// Define a callback function to process each presence in the range
	s.tree.AscendGreaterOrEqual(searchKey, func(item *PresenceChangeInfo) bool {
		presence := item

		// Stop iteration when we've passed the end of our target range
		if presence.PrSeq > to {
			return false
		}

		// Add this presence to the result
		result = append(result, presence.DeepCopy())

		return true
	})

	return result, nil
}

// GetMaxPrSeq returns the maximum presence sequence in the storage.
func (s *PresenceStorage) GetMaxPrSeq() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.tree.Len() == 0 {
		return 0
	}

	var maxPrSeq int64
	s.tree.Descend(func(item *PresenceChangeInfo) bool {
		maxPrSeq = item.PrSeq
		return false // Stop after first item (highest PrSeq)
	})

	return maxPrSeq
}

// GetMaxPrSeqForDoc returns the maximum presence sequence for a specific document.
func (s *PresenceStorage) GetMaxPrSeqForDoc(docRefKey types.DocRefKey) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.tree.Len() == 0 {
		return 0
	}

	var maxPrSeq int64
	s.tree.Descend(func(item *PresenceChangeInfo) bool {
		if item.ProjectID == docRefKey.ProjectID && item.DocID == docRefKey.DocID {
			maxPrSeq = item.PrSeq
			return false // Stop after first match (highest PrSeq for this doc)
		}
		return true // Continue searching
	})

	return maxPrSeq
}

// Size returns the number of presence infos stored.
func (s *PresenceStorage) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tree.Len()
}

// Clear removes all presence infos from the storage.
func (s *PresenceStorage) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tree = btree.NewG(32, func(a, b *PresenceChangeInfo) bool {
		if a.PrSeq != b.PrSeq {
			return a.PrSeq < b.PrSeq
		}
		if a.ProjectID != b.ProjectID {
			return a.ProjectID < b.ProjectID
		}
		return a.DocID < b.DocID
	})
}

// RemovePresencesBeforePrSeq removes all presences with PrSeq less than the given sequence.
// This can be used for cleanup/garbage collection.
func (s *PresenceStorage) RemovePresencesBeforePrSeq(prSeq int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var toDelete []*PresenceChangeInfo

	// Find all presences with PrSeq < prSeq
	s.tree.Ascend(func(item *PresenceChangeInfo) bool {
		if item.PrSeq < prSeq {
			toDelete = append(toDelete, item)
			return true
		}
		return false // Since tree is sorted by PrSeq, we can stop here
	})

	// Delete found items
	for _, item := range toDelete {
		s.tree.Delete(item)
	}

	return len(toDelete)
}

// calcMissingPresenceRanges calculates which presence ranges need to be fetched
// (this method is for potential future use when we need to fetch missing presence data)
func (s *PresenceStorage) calcMissingPresenceRanges(docRefKey types.DocRefKey, from, to int64) []PresenceRange {
	if s.tree.Len() == 0 {
		return []PresenceRange{{From: from, To: to}}
	}

	var missingRanges []PresenceRange
	var seqMap = make(map[int64]bool, to-from+1)

	// First, create a map of all sequence numbers in our target range
	for seq := from; seq <= to; seq++ {
		seqMap[seq] = false // false means not found yet
	}

	// Mark which sequences we already have for this document
	searchKey := &PresenceChangeInfo{
		ProjectID: docRefKey.ProjectID,
		DocID:     docRefKey.DocID,
		PrSeq:     from,
	}

	s.tree.AscendGreaterOrEqual(searchKey, func(item *PresenceChangeInfo) bool {
		presence := item
		seq := presence.PrSeq

		// Stop if we've gone past our target range
		if seq > to {
			return false
		}

		// Skip if not for the same document
		if presence.ProjectID != docRefKey.ProjectID || presence.DocID != docRefKey.DocID {
			return presence.PrSeq <= to
		}

		// Mark this sequence as found
		seqMap[seq] = true

		return true
	})

	// Now find contiguous missing ranges
	var inRange bool = false
	var startMissing int64

	for seq := from; seq <= to; seq++ {
		if !seqMap[seq] { // This sequence is missing
			if !inRange {
				// Start of a new missing range
				inRange = true
				startMissing = seq
			}
		} else if inRange { // We found a sequence but were in a missing range
			// End of missing range
			missingRanges = append(missingRanges, PresenceRange{
				From: startMissing,
				To:   seq - 1,
			})
			inRange = false
		}
	}

	// Handle case where missing range extends to the end
	if inRange {
		missingRanges = append(missingRanges, PresenceRange{
			From: startMissing,
			To:   to,
		})
	}

	// Merge adjacent ranges if needed
	if len(missingRanges) > 1 {
		return mergeAdjacentPresenceRanges(missingRanges)
	}

	return missingRanges
}

// mergeAdjacentPresenceRanges merges adjacent or overlapping presence ranges
func mergeAdjacentPresenceRanges(ranges []PresenceRange) []PresenceRange {
	if len(ranges) <= 1 {
		return ranges
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].From < ranges[j].From
	})

	result := make([]PresenceRange, 0, len(ranges))
	current := ranges[0]

	for i := 1; i < len(ranges); i++ {
		if ranges[i].From <= current.To+1 {
			current.To = max(current.To, ranges[i].To)
		} else {
			result = append(result, current)
			current = ranges[i]
		}
	}

	result = append(result, current)

	return result
}
