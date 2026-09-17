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

// newDayRange builds the half-open range [start, end), deriving Empty from the
// bounds so no caller has to remember to.
func newDayRange(start, end time.Time) dayRange {
	return dayRange{Start: start, End: end, Empty: !start.Before(end)}
}

// splitWindow cuts the requested window [from, to) against cov, the day range
// the summary can serve, into three half-open ranges:
//
//	pre   = [from, min(to, cov.Start))           from the base
//	hist  = [max(from, cov.Start), min(to, cov.End)) from the summary
//	fresh = [max(from, cov.End), to)             from the base
//
// The three are disjoint by day and their union is exactly [from, to), which is
// what keeps a subject counted once across the halves. Any of them may be
// Empty: a window inside the coverage has no pre and no fresh, one that ends
// before the summary starts is all pre, and one that starts after the summary
// ends is all fresh.
//
// pre is what keeps MAX(dt) from being read as a coverage set. A summary filled
// only for the last week — a table added to a cluster where the dual read is
// already on, whose refresh job wrote before its backfill — has a coverage that
// starts well after a 3-month window does, and the days below it have to come
// from the base rather than from a summary with no row for them. See
// coverage.go.
//
// An Empty cov means the summary covers nothing, so the whole window is fresh:
// the base serves it as it did before the summary existed.
func splitWindow(from, to time.Time, cov dayRange) (pre, hist, fresh dayRange) {
	floor, boundary := from, from
	if !cov.Empty {
		floor, boundary = cov.Start, cov.End
	}

	pEnd := to
	if floor.Before(pEnd) {
		pEnd = floor
	}
	pre = newDayRange(from, pEnd)

	hStart := from
	if floor.After(hStart) {
		hStart = floor
	}
	hEnd := to
	if boundary.Before(hEnd) {
		hEnd = boundary
	}
	hist = newDayRange(hStart, hEnd)

	fStart := from
	if boundary.After(fStart) {
		fStart = boundary
	}
	fresh = newDayRange(fStart, to)

	return pre, hist, fresh
}
