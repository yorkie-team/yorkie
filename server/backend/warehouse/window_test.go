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

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The steady state: the summary starts well before any window asked for here,
// so every window splits in two and the pre range stays empty.
func TestSplitWindow(t *testing.T) {
	cov := newDayRange(day("2000-01-01"), day("2026-08-31"))

	cases := []struct {
		name       string
		from, to   string
		histEmpty  bool
		freshEmpty bool
		hStart     string
		hEnd       string
		fStart     string
	}{
		{"entirely before the split", "2026-08-01", "2026-08-31", false, true, "2026-08-01", "2026-08-31", ""},
		{"entirely at or after the split", "2026-08-31", "2026-09-01", true, false, "", "", "2026-08-31"},
		{"straddling", "2026-08-01", "2026-09-01", false, false, "2026-08-01", "2026-08-31", "2026-08-31"},
		{"empty input", "2026-08-31", "2026-08-31", true, true, "", "", ""},
		{"future window", "2026-09-01", "2026-09-05", true, false, "", "", "2026-09-01"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pre, hist, fresh := splitWindow(day(c.from), day(c.to), cov)
			assert.True(t, pre.Empty, "pre.Empty: the summary starts before the window")
			assert.Equal(t, c.histEmpty, hist.Empty, "hist.Empty")
			assert.Equal(t, c.freshEmpty, fresh.Empty, "fresh.Empty")
			if !hist.Empty {
				assert.Equal(t, day(c.hStart), hist.Start, "hist.Start")
				assert.Equal(t, day(c.hEnd), hist.End, "hist.End")
			}
			if !fresh.Empty {
				assert.Equal(t, day(c.fStart), fresh.Start, "fresh.Start")
				assert.Equal(t, day(c.to), fresh.End, "fresh.End")
			}
		})
	}
}

// A summary that starts after the window does — the shape of a table added to a
// cluster where the dual read is already on, whose refresh job wrote its 7-day
// lookback before the one-time backfill ran. The days below its first row are
// as absent from it as the days above its last, so they belong to the base.
func TestSplitWindowBelowSummaryFloor(t *testing.T) {
	cov := newDayRange(day("2026-08-25"), day("2026-08-31"))

	cases := []struct {
		name                            string
		from, to                        string
		preEmpty, histEmpty, freshEmpty bool
		pEnd, hStart, hEnd, fStart      string
	}{
		{
			// The window reaches below the summary and past it: all three.
			name: "reaching below and above the coverage",
			from: "2026-08-01", to: "2026-09-01",
			pEnd: "2026-08-25", hStart: "2026-08-25", hEnd: "2026-08-31", fStart: "2026-08-31",
		},
		{
			// Entirely below the summary's first day: the base serves all of it.
			name: "entirely below the coverage",
			from: "2026-08-01", to: "2026-08-20",
			histEmpty: true, freshEmpty: true, pEnd: "2026-08-20",
		},
		{
			name: "starting below and ending inside the coverage",
			from: "2026-08-20", to: "2026-08-28",
			freshEmpty: true, pEnd: "2026-08-25", hStart: "2026-08-25", hEnd: "2026-08-28",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pre, hist, fresh := splitWindow(day(c.from), day(c.to), cov)
			assert.Equal(t, c.preEmpty, pre.Empty, "pre.Empty")
			assert.Equal(t, c.histEmpty, hist.Empty, "hist.Empty")
			assert.Equal(t, c.freshEmpty, fresh.Empty, "fresh.Empty")
			if !pre.Empty {
				assert.Equal(t, day(c.from), pre.Start, "pre.Start")
				assert.Equal(t, day(c.pEnd), pre.End, "pre.End")
			}
			if !hist.Empty {
				assert.Equal(t, day(c.hStart), hist.Start, "hist.Start")
				assert.Equal(t, day(c.hEnd), hist.End, "hist.End")
			}
			if !fresh.Empty {
				assert.Equal(t, day(c.fStart), fresh.Start, "fresh.Start")
				assert.Equal(t, day(c.to), fresh.End, "fresh.End")
			}
		})
	}
}

// An empty coverage means the summary holds nothing, so the whole window is
// read from the base — as the flag-off path reads it. It goes to fresh rather
// than pre so the emitted SQL is the one branch it has always been.
func TestSplitWindowEmptyCoverageIsAllFresh(t *testing.T) {
	from, to := day("2026-08-01"), day("2026-09-01")
	pre, hist, fresh := splitWindow(from, to, dayRange{Empty: true})

	assert.True(t, pre.Empty, "pre.Empty")
	assert.True(t, hist.Empty, "hist.Empty")
	require.False(t, fresh.Empty, "fresh.Empty")
	assert.Equal(t, from, fresh.Start)
	assert.Equal(t, to, fresh.End)
}

// The property the whole split rests on: the three ranges partition [from, to)
// exactly. Overlap would double count a subject in a total, and a missing day
// would draw as a zero. Checked day by day over every window-against-coverage
// combination below, so the arithmetic has nowhere to hide.
func TestSplitWindowPartitionsTheWindow(t *testing.T) {
	covs := map[string]dayRange{
		"empty":                 {Empty: true},
		"one day":               newDayRange(day("2026-08-10"), day("2026-08-11")),
		"inside the window":     newDayRange(day("2026-08-05"), day("2026-08-20")),
		"starting before":       newDayRange(day("2026-07-01"), day("2026-08-15")),
		"ending after":          newDayRange(day("2026-08-15"), day("2026-10-01")),
		"containing the window": newDayRange(day("2026-07-01"), day("2026-10-01")),
		"entirely before":       newDayRange(day("2026-06-01"), day("2026-07-01")),
		"entirely after":        newDayRange(day("2026-09-20"), day("2026-10-01")),
	}
	windows := map[string][2]string{
		"a month":                     {"2026-08-01", "2026-09-01"},
		"a single day":                {"2026-08-10", "2026-08-11"},
		"empty":                       {"2026-08-10", "2026-08-10"},
		"the day the coverage starts": {"2026-08-15", "2026-08-16"},
	}

	for covName, cov := range covs {
		for winName, w := range windows {
			t.Run(covName+"/"+winName, func(t *testing.T) {
				from, to := day(w[0]), day(w[1])
				pre, hist, fresh := splitWindow(from, to, cov)

				seen := map[string]string{}
				for _, r := range []struct {
					name string
					rng  dayRange
				}{{"pre", pre}, {"hist", hist}, {"fresh", fresh}} {
					if r.rng.Empty {
						assert.False(t, r.rng.Start.Before(r.rng.End), "%s is Empty but spans days", r.name)
						continue
					}
					require.True(t, r.rng.Start.Before(r.rng.End), "%s is not Empty but spans no day", r.name)
					for d := r.rng.Start; d.Before(r.rng.End); d = d.AddDate(0, 0, 1) {
						key := dayFmt(d)
						prev, dup := seen[key]
						require.False(t, dup, "%s is served by both %s and %s", key, prev, r.name)
						seen[key] = r.name
					}
				}

				want := map[string]string{}
				for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
					want[dayFmt(d)] = ""
				}
				assert.Len(t, seen, len(want), "the three ranges must cover the window exactly")
				for key := range want {
					_, ok := seen[key]
					assert.True(t, ok, "%s is served by no range", key)
				}

				// hist is the only range the summary serves, so it must stay
				// inside the coverage.
				if !hist.Empty && !cov.Empty {
					assert.False(t, hist.Start.Before(cov.Start), "hist starts below the coverage")
					assert.False(t, hist.End.After(cov.End), "hist ends above the coverage")
				}
			})
		}
	}
}
