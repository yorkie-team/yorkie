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
	"bytes"
	"errors"
	"unicode/utf16"
)

// ErrInvalidUTF16Index is returned when an index splits a UTF-16 surrogate pair.
var ErrInvalidUTF16Index = errors.New(
	"index must not split a UTF-16 surrogate pair",
)

const hex = "0123456789abcdef"

// isUTF16Boundary reports whether offset, counted in UTF-16 code units, is a
// valid boundary in value, i.e. it does not fall between the two units of a
// surrogate pair. It walks the runes instead of encoding the whole value, so
// it allocates nothing and stops as soon as it reaches offset.
func isUTF16Boundary(value string, offset int) bool {
	if offset <= 0 {
		return true
	}

	units := 0
	for _, r := range value {
		width := utf16.RuneLen(r)
		if width == 2 && units+1 == offset {
			return false
		}

		units += width
		if units >= offset {
			return true
		}
	}

	return true
}

// EscapeString returns a string that is safe to embed in a JSON document.
func EscapeString(s string) string {
	var buf bytes.Buffer

	l := len(s)
	for i := range l {
		c := s[i]
		if c >= 0x20 && c != '\\' && c != '"' {
			buf.WriteByte(c)
			continue
		}
		switch c {
		case '\\':
			buf.WriteByte('\\')
			buf.WriteByte('\\')
		case '"':
			buf.WriteByte('\\')
			buf.WriteByte('"')
		case '\n':
			buf.WriteByte('\\')
			buf.WriteByte('n')
		case '\f':
			buf.WriteByte('\\')
			buf.WriteByte('f')
		case '\b':
			buf.WriteByte('\\')
			buf.WriteByte('b')
		case '\r':
			buf.WriteByte('\\')
			buf.WriteByte('r')
		case '\t':
			buf.WriteByte('\\')
			buf.WriteByte('t')
		default:
			buf.WriteByte('\\')
			buf.WriteByte('u')
			buf.WriteByte('0')
			buf.WriteByte('0')
			buf.WriteByte(hex[c>>4])
			buf.WriteByte(hex[c&0xF])
		}
		continue
	}

	return buf.String()
}
