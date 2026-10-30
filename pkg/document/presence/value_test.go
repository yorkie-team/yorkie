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

package presence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

func TestEncodeValue(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"plain word", "moved", `"moved"`},
		{"empty", "", `""`},
		{"quote", `say "hi"`, `"say \"hi\""`},
		{"newline", "a\nb", `"a\nb"`},
		{"json text", `{"x":1}`, `"{\"x\":1}"`},
		// JSON.stringify leaves these alone; encoding/json escapes them
		// unless HTML escaping is turned off, which EncodeValue does.
		{"html", "<a>&</a>", `"<a>&</a>"`},
		{"non-ascii", "안녕", `"안녕"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded := presence.EncodeValue(test.value)
			assert.Equal(t, test.expected, encoded)
			assert.Equal(t, test.value, presence.DecodeValue(encoded))
		})
	}
}

func TestDecodeValue(t *testing.T) {
	// A value a JS peer wrote: JSON.stringify of a string.
	assert.Equal(t, "moved", presence.DecodeValue(`"moved"`))

	// A value a JS peer wrote that is not a string has no string to decode
	// to, so its JSON text is what a Go caller reads.
	assert.Equal(t, `{"x":1}`, presence.DecodeValue(`{"x":1}`))
	assert.Equal(t, "42", presence.DecodeValue("42"))
	assert.Equal(t, "true", presence.DecodeValue("true"))

	// A value written by a Go client before presence values were encoded is
	// not JSON at all, and stays readable as itself.
	assert.Equal(t, "moved", presence.DecodeValue("moved"))
	assert.Equal(t, "", presence.DecodeValue(""))
}

func TestDecodeData(t *testing.T) {
	assert.Nil(t, presence.DecodeData(nil))
	assert.Equal(t, presence.Data{}, presence.DecodeData(presence.Data{}))

	assert.Equal(t, presence.Data{"cursor": "moved", "legacy": "raw"}, presence.DecodeData(
		presence.Data{"cursor": `"moved"`, "legacy": "raw"},
	))

	assert.Equal(t, map[string]presence.Data{
		"c1": {"name": "alice"},
		"c2": nil,
	}, presence.DecodeDataMap(map[string]presence.Data{
		"c1": {"name": `"alice"`},
		"c2": nil,
	}))
}
