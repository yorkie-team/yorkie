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

package converter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestPresenceEncodeGrowthMatchesEncoding pins the growth count to the encoder
// it predicts. The count exists so that an input the repair budget cannot
// afford is never built, which only holds if it is exact: over-counting
// discards values the budget should have covered, and under-counting lets the
// budget be overspent.
func TestPresenceEncodeGrowthMatchesEncoding(t *testing.T) {
	values := []string{
		"",
		"moved",
		`{"from":1,"to":2}`,
		`a {[ b \" c`,
		"한글 레이블",
		"tab\tnewline\nreturn\rbackspace\bformfeed\f",
		"\u2028\u2029",
		"\xff\xfe\xed\xa0\x80",
		strings.Repeat("\x01", 128),
		strings.Repeat("a", 4096),
	}
	for b := range 256 {
		values = append(values, string([]byte{byte(b)}), "x"+string([]byte{byte(b)})+"y")
	}

	for _, value := range values {
		want := len(presence.EncodeValue(value)) - len(value)
		assert.Equal(t, want, presenceEncodeGrowth(value), "growth of %q", value)
	}
}
