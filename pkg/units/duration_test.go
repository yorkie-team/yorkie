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

package units_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/units"
)

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		desc     string
		input    time.Duration
		expected string
	}{
		{"zero", 0, "Less than a second"},
		{"sub-second", 450 * time.Millisecond, "Less than a second"},
		{"negative", -time.Second, "Less than a second"},
		{"one second", time.Second, "1 second"},
		{"a second and a half truncates", 1500 * time.Millisecond, "1 second"},
		{"two seconds", 2 * time.Second, "2 seconds"},
		{"one below a minute", 59 * time.Second, "59 seconds"},
		{"exactly a minute", time.Minute, "About a minute"},
		{"one below two minutes", 119 * time.Second, "About a minute"},
		{"two minutes", 2 * time.Minute, "2 minutes"},
		{"one below an hour", 59 * time.Minute, "59 minutes"},
		{"exactly an hour", time.Hour, "About an hour"},
		// The hour count is rounded, not truncated: 89 minutes rounds down to
		// one hour and 90 minutes rounds up to two.
		{"rounds down to an hour", 89 * time.Minute, "About an hour"},
		{"rounds up to two hours", 90 * time.Minute, "2 hours"},
		{"hours", 3 * time.Hour, "3 hours"},
		{"one below two days", 47 * time.Hour, "47 hours"},
		{"exactly two days", 48 * time.Hour, "2 days"},
		{"days", 72 * time.Hour, "3 days"},
		{"one below two weeks", 24*7*2*time.Hour - time.Hour, "13 days"},
		{"exactly two weeks", 24 * 7 * 2 * time.Hour, "2 weeks"},
		{"weeks", 24 * 7 * 3 * time.Hour, "3 weeks"},
		{"one below two months", 24*30*2*time.Hour - time.Hour, "8 weeks"},
		{"exactly two months", 24 * 30 * 2 * time.Hour, "2 months"},
		{"months", 24 * 30 * 5 * time.Hour, "5 months"},
		{"one below two years", 24*365*2*time.Hour - time.Hour, "24 months"},
		{"exactly two years", 24 * 365 * 2 * time.Hour, "2 years"},
		{"years", 24 * 365 * 5 * time.Hour, "5 years"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			assert.Equal(t, test.expected, units.HumanDuration(test.input))
		})
	}
}
