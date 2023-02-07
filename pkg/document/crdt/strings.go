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

import "bytes"

const hex = "0123456789abcdef"

// EscapeString returns a string that is safe to embed in a JSON document.
func EscapeString(s string) string {
	var buf bytes.Buffer

	l := len(s)
	for i := 0; i < l; i++ {
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
