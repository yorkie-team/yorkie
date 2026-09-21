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

package document_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// What #2003 closes, and the one case it cannot.
//
// The issue was filed here "because the question is which representation is
// canonical -- deciding that is a protocol-level call". It is now this one: the
// JS SDK stores an ordinary string as itself, the way Style's map[string]string
// always has, so `color="red"` puts the same three bytes on the wire from
// either SDK and each reads back exactly what the other wrote.
//
// One case is irreducible without a value-kind field on the wire. A JS string
// that is ITSELF a JSON document -- '1', 'true', 'null' -- keeps its quotes
// there, because stored raw it could not be told from the number or boolean it
// encodes. This SDK sees those quotes as part of the value, and cannot express
// the distinction at all. These tests pin that, so the gap is a fact in the
// suite and whoever closes it gets a failing test naming what changed.

// styleValue returns what the document holds for the given attribute value,
// as an ordinary Go client would read it back.
func styleValue(t *testing.T, value string) string {
	t.Helper()

	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
			{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
		}})
		root.GetTree("t").Style(0, 1, map[string]string{"color": value})
		return nil
	}))

	return doc.Root().GetTree("t").ToXML()
}

// An ordinary string now round-trips between the two SDKs unchanged: the JS
// side stores `red` for the string 'red', which is exactly what this one holds
// for map[string]string{"color": "red"}.
func TestAnOrdinaryStringAttributeAgreesAcrossSDKs(t *testing.T) {
	for _, value := range []string{"red", "Arial", "#fff", "bold italic"} {
		require.Equal(t,
			`<doc><p color="`+value+`">ab</p></doc>`,
			styleValue(t, value),
			"a JS client stores %q for the same attribute", value)
	}
}

// So does every non-string: the JS side's JSON form is byte-identical to what
// this SDK holds for the equivalent string.
func TestNonStringAttributesAgreeAcrossSDKs(t *testing.T) {
	for _, value := range []string{"true", "12", "1.5", "null"} {
		require.Equal(t,
			`<doc><p color="`+value+`">ab</p></doc>`,
			styleValue(t, value),
			"a JS client stores %q for the equivalent non-string", value)
	}
}

// THE REMAINING GAP. A JS string that is itself JSON keeps its quotes there, so
// this SDK reads them as part of the value. Closing it needs a value-kind field
// on the wire -- a protocol change on both SDKs and the server, which still
// leaves a default for every value already stored.
func TestAJSStringThatLooksLikeJSONStillDiffers(t *testing.T) {
	// What the JS SDK stores for the STRING 'true', to keep it a string there.
	require.Equal(t, `<doc><p color="\"true\"">ab</p></doc>`, styleValue(t, `"true"`))

	// What this SDK stores for "true", and what JS stores for the BOOLEAN true.
	require.Equal(t, `<doc><p color="true">ab</p></doc>`, styleValue(t, "true"))
}
