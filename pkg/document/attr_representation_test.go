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

// The half of #2003 that is still open.
//
// The issue says it was filed here "because the question is which
// representation is canonical -- deciding that is a protocol-level call, and
// whichever way it goes one of the two implementations changes". That decision
// has not been made. The JS SDK's reading and sizing were brought into line
// with this one without it; what goes on the wire was left alone, because
// storing strings raw there makes a caller's string '1' read back as the
// number 1.
//
// So a string attribute written by a JS client still arrives here wrapped in
// the quotes that SDK's JSON encoding added, and this SDK has no idea it
// should take them off -- Style takes map[string]string and stores what it is
// given. These tests pin that, so the gap is a fact in the suite rather than a
// sentence in a pull request, and so whoever closes it finds a failing test
// that names exactly what changed.

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

func TestAJSAuthoredStringAttributeKeepsItsQuotesHere(t *testing.T) {
	// `red` is what this SDK stores for Style(..., {"color": "red"}).
	require.Equal(t, `<doc><p color="red">ab</p></doc>`, styleValue(t, "red"))

	// `"red"` is what the JS SDK stores for the same logical attribute: its
	// json/ boundary JSON-encodes every value before it reaches the CRDT. This
	// SDK renders the quotes, because to it they are part of the value.
	//
	// Change this assertion when the representation is unified -- and note
	// which direction was chosen, because it decides which SDK's existing
	// documents need rewriting.
	require.Equal(t, `<doc><p color="\"red\"">ab</p></doc>`, styleValue(t, `"red"`))
}

// The split is strings only, which is what makes it narrow enough to close.
// The JS SDK stores `true` for the boolean true and `12` for the number 12 --
// byte-identical to what this SDK holds for the equivalent string -- so those
// already agree and need no decision.
func TestNonStringAttributesAlreadyAgreeAcrossSDKs(t *testing.T) {
	for _, value := range []string{"true", "12", "1.5", "null"} {
		require.Equal(t,
			`<doc><p color="`+value+`">ab</p></doc>`,
			styleValue(t, value),
			"a JS client stores %q for the equivalent non-string", value)
	}
}
