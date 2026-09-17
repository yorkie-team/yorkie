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

package projects

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
)

// The window's peak total is derived from the series instead of being queried
// separately, so the derivation has to hold on its own: the maximum over the
// whole series, wherever in the series it falls, and zero for a project with no
// traffic in the window.
func TestMaxMetricValue(t *testing.T) {
	point := func(value int) types.MetricPoint {
		return types.MetricPoint{Time: 0, Value: value}
	}

	tests := []struct {
		name   string
		points []types.MetricPoint
		want   int
	}{
		{
			// No rows at all: the dashboard shows 0, not a stale or negative
			// number.
			name:   "empty series",
			points: nil,
			want:   0,
		},
		{
			name:   "single point",
			points: []types.MetricPoint{point(7)},
			want:   7,
		},
		{
			// The peak may sit anywhere in the window, so the last day's value
			// is not the answer.
			name:   "max is not the last point",
			points: []types.MetricPoint{point(3), point(11), point(4)},
			want:   11,
		},
		{
			name:   "max is the first point",
			points: []types.MetricPoint{point(11), point(3), point(4)},
			want:   11,
		},
		{
			// A window whose every day is empty is still a peak of zero.
			name:   "all zeroes",
			points: []types.MetricPoint{point(0), point(0)},
			want:   0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, maxMetricValue(tc.points))
		})
	}
}
