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

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// decodeAlways is what logicalSize means, spelled out without any of the
// short-circuits: hand every quoted value to the decoder and fall back to the
// stored length when it will not decode. logicalSize has to agree with it on
// every input, including the ones a hostile client picks; what it may not do
// is run it on the hot path.
func decodeAlways(val string) int {
	if len(val) < 2 || val[0] != '"' {
		return len(val)
	}

	var decoded string
	if err := json.Unmarshal([]byte(val), &decoded); err != nil {
		return len(val)
	}

	return len(decoded)
}

func TestLogicalSize(t *testing.T) {
	for _, val := range []string{
		"",
		"a",
		`"`,
		`""`,
		"plain value",
		`{"a":1}`,
		`"1"`,
		`"quoted"`,
		`"with \"escape\""`,
		`"tab\tseparated"`,
		`"A"`,
		`"😀"`,
		`"\uD83D"`,
		// A value that defeats the leading-quote check and then fails to
		// decode: the charge must be the stored length, and getting there
		// must not cost a decode.
		`"unterminated`,
		`"` + strings.Repeat("x", 1024),
		// Bare quote and raw control characters make the value invalid JSON.
		`"a"b"`,
		"\"a\nb\"",
		// Whitespace is legal after a JSON value.
		`"padded" `,
		`"padded"` + "\t\r\n",
		` "leading"`,
	} {
		assert.Equal(t, decodeAlways(val), logicalSize(val), "value: %q", val)
	}
}

// TestRHTNodeDataSizeIsPrecomputed pins that the value length a node charges
// is measured when the node is built and carried, not measured again on every
// DataSize call and not recomputed by DeepCopy.
func TestRHTNodeDataSizeIsPrecomputed(t *testing.T) {
	node := newRHTNode("k", `"A"`, nil, false)
	assert.Equal(t, 1, node.valLen)

	// Rewriting the stored value behind the node's back leaves the charge
	// alone: nothing reads the value on the DataSize path.
	node.val = `"AB"`
	assert.Equal(t, (len("k")+1)*2, node.DataSize().Data)
}
