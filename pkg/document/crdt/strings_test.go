/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package crdt

import (
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
)

func TestEscapeString(t *testing.T) {
	t.Run("escape normal string", func(t *testing.T) {
		str := `hello world`
		expected := `hello world`
		actual := EscapeString(str)
		assert.Equal(t, expected, actual)
	})

	t.Run("escape string with backslash, doublequote and control characters", func(t *testing.T) {
		str := "hello world\"\\\n\f\b\r\t"
		expected := `hello world\"\\\n\f\b\r\t`
		actual := EscapeString(str)
		assert.Equal(t, expected, actual)
	})

	t.Run("escape string with unicode characters", func(t *testing.T) {
		str := "hello world\u1234\u6645"
		expected := "hello world\u1234\u6645"
		actual := EscapeString(str)
		assert.Equal(t, expected, actual)
	})
}

func TestIsUTF16Boundary(t *testing.T) {
	// encodedBoundary is the straightforward definition: encode the value to
	// UTF-16 and check whether offset falls between a high and a low
	// surrogate.
	encodedBoundary := func(value string, offset int) bool {
		units := utf16.Encode([]rune(value))
		if offset <= 0 || offset >= len(units) {
			return true
		}
		isHigh := units[offset-1] >= 0xD800 && units[offset-1] < 0xDC00
		isLow := units[offset] >= 0xDC00 && units[offset] < 0xE000
		return !isHigh || !isLow
	}

	values := []string{
		"",
		"abc",
		"가나다",
		"\U0001F600",
		"\U0001F600x",
		"x\U0001F600",
		"\U0001F600\U0001F601",
		"a\U0001F600b\U0001F601c",
		"\xff\U0001F600",
	}

	t.Run("matches the encoded definition at every offset test", func(t *testing.T) {
		for _, value := range values {
			n := len(utf16.Encode([]rune(value)))
			for offset := -1; offset <= n+1; offset++ {
				assert.Equal(t, encodedBoundary(value, offset), isUTF16Boundary(value, offset),
					"value %q, offset %d", value, offset)
			}
		}
	})

	t.Run("rejects only the offset inside a surrogate pair test", func(t *testing.T) {
		value := "a\U0001F600b"
		assert.True(t, isUTF16Boundary(value, 1))
		assert.False(t, isUTF16Boundary(value, 2))
		assert.True(t, isUTF16Boundary(value, 3))
	})

	t.Run("does not allocate test", func(t *testing.T) {
		value := "a\U0001F600b\U0001F601c"
		allocs := testing.AllocsPerRun(100, func() {
			_ = isUTF16Boundary(value, 5)
		})
		assert.Zero(t, allocs)
	})
}
