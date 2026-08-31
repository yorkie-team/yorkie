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
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSplitWindow(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		assert.NoError(t, err)
		return d
	}
	today := day("2026-08-31")

	cases := []struct {
		name       string
		from, to   string
		histEmpty  bool
		freshEmpty bool
		hEnd       string
		fStart     string
	}{
		{"entirely past", "2026-08-01", "2026-08-31", false, true, "2026-08-31", ""},
		{"entirely today", "2026-08-31", "2026-09-01", true, false, "", "2026-08-31"},
		{"straddling", "2026-08-01", "2026-09-01", false, false, "2026-08-31", "2026-08-31"},
		{"empty input", "2026-08-31", "2026-08-31", true, true, "", ""},
		{"future window", "2026-09-01", "2026-09-05", true, false, "", "2026-09-01"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hist, fresh := splitWindow(day(c.from), day(c.to), today)
			assert.Equal(t, c.histEmpty, hist.Empty, "hist.Empty")
			assert.Equal(t, c.freshEmpty, fresh.Empty, "fresh.Empty")
			if !hist.Empty {
				assert.Equal(t, day(c.from), hist.Start, "hist.Start")
				assert.Equal(t, day(c.hEnd), hist.End, "hist.End")
			}
			if !fresh.Empty {
				assert.Equal(t, day(c.fStart), fresh.Start, "fresh.Start")
				assert.Equal(t, day(c.to), fresh.End, "fresh.End")
			}
		})
	}
}
