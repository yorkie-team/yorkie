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

package crdt

import "unicode/utf16"

// IsMidSurrogate reports whether the given UTF-16 offset falls between the two
// code units of a surrogate pair in value.
//
// Splitting there is the one index at which the two SDKs cannot agree on the
// result: Go holds its strings as UTF-8, so each lone half has to become
// U+FFFD on the way back out of utf16.Decode, while JS keeps the half as-is.
// The structure still converges -- both sides measure the same lengths and
// mint the same node IDs -- but the text does not. Callers reject such an
// index rather than let the split happen.
func IsMidSurrogate(value string, offset int) bool {
	if offset <= 0 {
		return false
	}

	encoded := utf16.Encode([]rune(value))
	if offset >= len(encoded) {
		return false
	}

	// utf16.IsSurrogate is true for either half, so the halves are tested by
	// range: only high-then-low is a pair this offset would cut in two.
	return encoded[offset-1] >= 0xD800 && encoded[offset-1] <= 0xDBFF &&
		encoded[offset] >= 0xDC00 && encoded[offset] <= 0xDFFF
}
