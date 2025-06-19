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
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// ChangeRange represents a range of changes for a specific document
type ChangeRange struct {
	From      int64                  // Starting server sequence
	To        int64                  // Ending server sequence
	Changes   []*database.ChangeInfo // The actual changes within this range
	CreatedAt gotime.Time            // When this range was cached (for future TTL implementation)
}

// Contains checks if the given sequence is within this range
func (r *ChangeRange) Contains(seq int64) bool {
	return seq >= r.From && seq <= r.To
}

// ContainsRange checks if this range fully contains another range
func (r *ChangeRange) ContainsRange(from, to int64) bool {
	return r.From <= from && r.To >= to
}

// Overlaps checks if this range overlaps with another range
func (r *ChangeRange) Overlaps(from, to int64) bool {
	return !(r.To < from || r.From > to)
}

// DocRangeStoreKey generates a store key for a document
func DocRangeStoreKey(docRefKey types.DocRefKey) string {
	return fmt.Sprintf("%s:%s", docRefKey.ProjectID, docRefKey.DocID)
}

// ChangeStore manages stored ranges of document changes
type ChangeStore struct {
	mu     sync.RWMutex
	ranges []*ChangeRange
}

// NewChangeStore creates a new instance of ChangeStore.
func NewChangeStore() *ChangeStore {
	return &ChangeStore{
		ranges: make([]*ChangeRange, 0),
	}
}

// AddRange adds a new range of changes to the store
func (s *ChangeStore) AddRange(r *ChangeRange) {
	// Don't add empty ranges
	if len(r.Changes) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Insert directly into the sorted position
	if len(s.ranges) == 0 {
		s.ranges = append(s.ranges, r)
		return
	}

	// Find insertion point using binary search
	idx := s.findInsertionPoint(r)

	// Make space for insertion
	s.ranges = append(s.ranges, nil)

	// Shift elements after insertion point by one position
	copy(s.ranges[idx+1:], s.ranges[idx:])

	// Insert at the appropriate position
	s.ranges[idx] = r
}

// findInsertionPoint finds the right position to insert a new range using binary search
func (s *ChangeStore) findInsertionPoint(r *ChangeRange) int {
	// Always assume the array is sorted - find insertion point using binary search
	left, right := 0, len(s.ranges)-1
	for left <= right {
		mid := left + (right-left)/2
		if s.ranges[mid].From == r.From {
			return mid
		} else if s.ranges[mid].From < r.From {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}

	return left
}

// CalculateMissingRanges calculates which ranges need to be fetched
func (s *ChangeStore) CalculateMissingRanges(from, to int64) []struct{ From, To int64 } {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.ranges) == 0 {
		return []struct{ From, To int64 }{{From: from, To: to}}
	}

	var missingRanges []struct{ From, To int64 }
	currentPos := from

	for _, r := range s.ranges {
		if r.To < currentPos {
			continue
		}

		if r.From > currentPos {
			missingRanges = append(missingRanges, struct{ From, To int64 }{
				From: currentPos,
				To:   r.From - 1,
			})
		}

		if r.To >= currentPos {
			currentPos = r.To + 1
		}

		if currentPos > to {
			return missingRanges
		}
	}

	if currentPos <= to {
		missingRanges = append(missingRanges, struct{ From, To int64 }{
			From: currentPos,
			To:   to,
		})
	}

	if len(missingRanges) > 1 {
		return mergeAdjacentRanges(missingRanges)
	}

	return missingRanges
}

// mergeAdjacentRanges merges adjacent or overlapping ranges
func mergeAdjacentRanges(ranges []struct{ From, To int64 }) []struct{ From, To int64 } {
	if len(ranges) <= 1 {
		return ranges
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].From < ranges[j].From
	})

	result := make([]struct{ From, To int64 }, 0, len(ranges))
	current := ranges[0]

	for i := 1; i < len(ranges); i++ {
		if ranges[i].From <= current.To+1 {
			if ranges[i].To > current.To {
				current.To = ranges[i].To
			}
		} else {
			result = append(result, current)
			current = ranges[i]
		}
	}

	result = append(result, current)

	return result
}

// ChangesInRange retrieves all changes within the specified range
func (s *ChangeStore) ChangesInRange(from, to int64) []*database.ChangeInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return empty result if no ranges exist
	if len(s.ranges) == 0 {
		return nil
	}

	// First select only relevant ranges using binary search
	relevantRanges := s.findRelevantRanges(from, to)

	// Estimate result size for memory allocation optimization
	estimatedSize := 0
	for _, r := range relevantRanges {
		estimatedSize += len(r.Changes)
	}

	// Pre-allocate result slice to minimize reallocation
	result := make([]*database.ChangeInfo, 0, estimatedSize)

	// Collect changes only from relevant ranges
	for _, r := range relevantRanges {
		for _, change := range r.Changes {
			if change.ServerSeq >= from && change.ServerSeq <= to {
				result = append(result, change)
			}
		}
	}

	return result
}

// findRelevantRanges finds ranges that might contain changes in the requested range
// Uses binary search to efficiently find relevant ranges
func (s *ChangeStore) findRelevantRanges(from, to int64) []*ChangeRange {
	// Handle empty slice case
	if len(s.ranges) == 0 {
		return nil
	}

	// Return empty result if no ranges can possibly overlap with the request
	if s.ranges[0].From > to || s.ranges[len(s.ranges)-1].To < from {
		return nil
	}

	// Binary search to find starting point: first range with From value >= from
	startIdx := 0
	left, right := 0, len(s.ranges)-1
	for left <= right {
		mid := left + (right-left)/2
		if s.ranges[mid].From >= from {
			startIdx = mid
			right = mid - 1
		} else {
			left = mid + 1
		}
	}

	// If all ranges are less than from, start checking from the last range
	if startIdx == 0 && s.ranges[0].From > from {
		startIdx = len(s.ranges) - 1
	}

	// Find first range that starts before 'from' but overlaps with 'to'
	for i := startIdx; i >= 0; i-- {
		if s.ranges[i].To < from {
			startIdx = i + 1
			break
		}
		startIdx = i
		if i == 0 {
			break
		}
	}

	// Collect relevant ranges
	var relevantRanges []*ChangeRange
	for i := startIdx; i < len(s.ranges); i++ {
		if s.ranges[i].From > to {
			break
		}
		if s.ranges[i].Overlaps(from, to) {
			relevantRanges = append(relevantRanges, s.ranges[i])
		}
	}

	return relevantRanges
}

// HasCompleteRange checks if the store has the complete range of sequences
func (s *ChangeStore) HasCompleteRange(from, to int64) bool {
	// Note: We don't need a mutex here as CalculateMissingRanges already has its own lock
	missing := s.CalculateMissingRanges(from, to)
	return len(missing) == 0
}
