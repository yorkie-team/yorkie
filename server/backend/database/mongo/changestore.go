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
	"fmt"
	"sort"
	"sync"

	"github.com/google/btree"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var (
	// ErrInvalidServerSeq is returned when the server sequence is invalid.
	ErrInvalidServerSeq = errors.Internal("invalid server sequence").WithCode("ErrInvalidServerSeq")
)

// ChangeRange represents a range of server sequences from start to end
type ChangeRange struct {
	From int64 // Starting server sequence
	To   int64 // Ending server sequence
}

// ChangeStore manages individual document changes using B-tree. It allows
// efficient storage and retrieval of changes based on their server sequence numbers.
type ChangeStore struct {
	mu     sync.RWMutex
	ranges []ChangeRange
	tree   *btree.BTreeG[*database.ChangeInfo]
}

// NewChangeStore creates a new instance of ChangeStore.
func NewChangeStore() *ChangeStore {
	return &ChangeStore{
		tree: btree.NewG(32, func(a, b *database.ChangeInfo) bool {
			return a.ServerSeq < b.ServerSeq
		}),
	}
}

// EnsureChanges ensures that all changes in the specified range are present in the store.
func (s *ChangeStore) EnsureChanges(
	from,
	to int64,
	fetcher func(from, to int64) ([]*database.ChangeInfo, error),
) error {
	if from > to {
		return fmt.Errorf("from (%d) > to (%d): %w", from, to, ErrInvalidServerSeq)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Calculate what ranges we need to fetch
	missingRanges := s.calcMissingRanges(from, to)
	if len(missingRanges) == 0 {
		return nil
	}

	// 2. Fetch and add each missing range
	for _, r := range missingRanges {
		changes, err := fetcher(r.From, r.To)
		if err != nil {
			return fmt.Errorf("fetch changes [%d-%d]: %w", r.From, r.To, err)
		}

		// 3. Add each change individually to the tree
		for _, change := range changes {
			s.tree.ReplaceOrInsert(change)
		}

		// 4. Mark this requested range as fetched so we don't re-request
		// sequences inside it again even if some sequence numbers weren't
		// present in the returned changes (i.e. "missing server seq").
		s.ranges = mergeAdjacentRanges(append(s.ranges, r))
	}

	return nil
}

// ChangesInRange retrieves all changes within the specified range
func (s *ChangeStore) ChangesInRange(from, to int64) []*database.ChangeInfo {
	if from > to {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return empty result if the tree is empty
	if s.tree.Len() == 0 {
		return nil
	}

	var result []*database.ChangeInfo

	// Create a dummy change with the minimum sequence as search key
	searchKey := &database.ChangeInfo{ServerSeq: from}

	// Define a callback function to process each change in the range
	s.tree.AscendGreaterOrEqual(searchKey, func(item *database.ChangeInfo) bool {
		change := item

		// Stop iteration when we've passed the end of our target range
		if change.ServerSeq > to {
			return false
		}

		// Add this change to the result
		result = append(result, change)

		return true
	})

	return result
}

// RemoveChangesByActor removes presence changes for the given actor,
// keeping Clear changes so the final state remains cleared.
func (s *ChangeStore) RemoveChangesByActor(actorID types.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var toDelete []*database.ChangeInfo
	s.tree.Ascend(func(item *database.ChangeInfo) bool {
		if item != nil && item.ActorID == actorID && !item.PresenceChange.IsClear() {
			toDelete = append(toDelete, item)
		}
		return true
	})

	for _, item := range toDelete {
		s.tree.Delete(item)
	}
}

// ReplaceOrInsert replaces or inserts the given changes into the store.
func (s *ChangeStore) ReplaceOrInsert(changes []*database.ChangeInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, pch := range changes {
		s.tree.ReplaceOrInsert(pch)
	}
}

// calcMissingRanges calculates which ranges need to be fetched
func (s *ChangeStore) calcMissingRanges(from, to int64) []ChangeRange {
	if s.tree.Len() == 0 {
		// If tree is empty but we've already fetched some ranges, mark those
		// fetched ranges as present to avoid refetching them. Otherwise,
		// request full range.
		if len(s.ranges) == 0 {
			return []ChangeRange{{From: from, To: to}}
		}
		// fallthrough to create seqMap and mark fetched ranges
	}

	var missingRanges []ChangeRange
	var seqMap = make(map[int64]bool, to-from+1)

	// First, create a map of all sequence numbers in our target range
	for seq := from; seq <= to; seq++ {
		seqMap[seq] = false // false means not found yet
	}

	// Mark which sequences we already have
	searchKey := &database.ChangeInfo{ServerSeq: from}
	s.tree.AscendGreaterOrEqual(searchKey, func(item *database.ChangeInfo) bool {
		change := item
		seq := change.ServerSeq

		// Stop if we've gone past our target range
		if seq > to {
			return false
		}

		// Mark this sequence as found
		seqMap[seq] = true

		return true
	})

	// Also mark sequences within fetchedRanges as found so we don't request
	// them again. This covers the case where the fetcher returned fewer
	// changes than requested (holes) and we want to avoid re-fetching the
	// same range repeatedly.
	for _, fr := range s.ranges {
		// ignore fetched ranges that don't overlap our target
		if fr.To < from || fr.From > to {
			continue
		}

		start := max(fr.From, from)
		end := min(fr.To, to)
		for seq := start; seq <= end; seq++ {
			seqMap[seq] = true
		}
	}

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
			missingRanges = append(missingRanges, ChangeRange{
				From: startMissing,
				To:   seq - 1,
			})
			inRange = false
		}
	}

	// Handle case where missing range extends to the end
	if inRange {
		missingRanges = append(missingRanges, ChangeRange{
			From: startMissing,
			To:   to,
		})
	}

	// Merge adjacent ranges if needed
	if len(missingRanges) > 1 {
		return mergeAdjacentRanges(missingRanges)
	}

	return missingRanges
}

// mergeAdjacentRanges merges adjacent or overlapping ranges
func mergeAdjacentRanges(ranges []ChangeRange) []ChangeRange {
	if len(ranges) <= 1 {
		return ranges
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].From < ranges[j].From
	})

	result := make([]ChangeRange, 0, len(ranges))
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
