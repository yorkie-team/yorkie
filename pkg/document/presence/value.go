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

package presence

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Presence values travel the wire as a map of strings, and every SDK reads
// each value as JSON: the JS SDK stringifies on encode and parses on decode
// (`packages/sdk/src/api/converter.ts`). A Go client that put a bare string
// such as `moved` on the wire made every JS peer fail to decode the pack
// that carried it, so the two functions below are where Go joins that
// contract -- EncodeValue on the way out, DecodeValue on the way back in.

// EncodeValue returns the JSON encoding of the given value, which is the form
// presence values take on the wire. It is Go's spelling of JS's
// `JSON.stringify(value)` for a string: HTML escaping is off so that the two
// SDKs put the same bytes on the wire for the same string.
func EncodeValue(value string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	// A string is always encodable, so the error is unreachable; Encode only
	// fails on values json cannot represent, such as a channel or a NaN.
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}

	// Encode appends a newline that JSON.stringify does not.
	return strings.TrimSuffix(buf.String(), "\n")
}

// DecodeValue returns the string a JSON-encoded presence value stands for.
//
// A value encoded from a string -- what EncodeValue writes and what a JS peer
// writes for `set({key: 'value'})` -- decodes to that string. Anything else is
// returned as it arrived: a JS peer's object or number has no string to decode
// to, and a value written by a Go client before this encoding existed is not
// JSON at all. Both stay readable as the text on the wire.
func DecodeValue(encoded string) string {
	var decoded string
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		return encoded
	}

	return decoded
}

// DecodeData returns a copy of the given presence data with every value
// decoded by DecodeValue. A nil presence decodes to nil, so a caller can
// still tell a client with no presence from one with an empty presence.
func DecodeData(data Data) Data {
	if data == nil {
		return nil
	}

	decoded := make(Data, len(data))
	for key, value := range data {
		decoded[key] = DecodeValue(value)
	}

	return decoded
}

// DecodeDataMap returns a copy of the given client-to-presence map with every
// presence decoded by DecodeData.
func DecodeDataMap(dataMap map[string]Data) map[string]Data {
	decoded := make(map[string]Data, len(dataMap))
	for clientID, data := range dataMap {
		decoded[clientID] = DecodeData(data)
	}

	return decoded
}
