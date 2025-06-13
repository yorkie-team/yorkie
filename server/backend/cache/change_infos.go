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

package cache

import (
	"sort"
	"sync"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

type ChangeInfos struct {
	mu      sync.RWMutex
	changes []*database.ChangeInfo
}

func NewChangeInfos() *ChangeInfos {
	return &ChangeInfos{}
}

func (ci *ChangeInfos) GetRange(from, to int64) ([]*database.ChangeInfo, bool) {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	start := sort.Search(len(ci.changes), func(i int) bool { return ci.changes[i].ServerSeq >= from })
	end := sort.Search(len(ci.changes), func(i int) bool { return ci.changes[i].ServerSeq > to })

	if start >= len(ci.changes) || ci.changes[start].ServerSeq > to {
		return nil, false
	}
	return append([]*database.ChangeInfo(nil), ci.changes[start:end]...), true
}

func (ci *ChangeInfos) Merge(newChanges []*database.ChangeInfo) {
	if len(newChanges) == 0 {
		return
	}
	sort.Slice(newChanges, func(i, j int) bool {
		return newChanges[i].ServerSeq < newChanges[j].ServerSeq
	})

	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.changes = mergeSorted(ci.changes, newChanges)
}

func mergeSorted(a, b []*database.ChangeInfo) []*database.ChangeInfo {
	i, j := 0, 0
	out := make([]*database.ChangeInfo, 0, len(a)+len(b))

	for i < len(a) && j < len(b) {
		if a[i].ServerSeq < b[j].ServerSeq {
			out = append(out, a[i])
			i++
		} else if a[i].ServerSeq > b[j].ServerSeq {
			out = append(out, b[j])
			j++
		} else {
			out = append(out, b[j]) // 최신으로 덮음
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}
