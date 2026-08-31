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

package warehouse

import "time"

// dayRange is a half-open [Start, End) UTC day range. Empty is true when the
// range covers no day, i.e. Start >= End.
type dayRange struct {
	Start time.Time
	End   time.Time
	Empty bool
}

// splitWindow splits the requested window [from, to) at the UTC day boundary
// today into two half-open ranges: a historical range served by the daily
// summary and a fresh range served by the base rollup. The two never overlap
// by day, so their union is exact.
//
// hist  = [from, min(to, today))
// fresh = [max(from, today), to)
//
// Either range may be Empty: a window entirely in the past has an empty fresh
// range, and a window entirely within today has an empty historical range.
func splitWindow(from, to, today time.Time) (hist, fresh dayRange) {
	hEnd := to
	if today.Before(hEnd) {
		hEnd = today
	}
	hist = dayRange{Start: from, End: hEnd, Empty: !from.Before(hEnd)}

	fStart := from
	if today.After(fStart) {
		fStart = today
	}
	fresh = dayRange{Start: fStart, End: to, Empty: !fStart.Before(to)}

	return hist, fresh
}
