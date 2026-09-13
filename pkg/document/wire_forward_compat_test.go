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
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// `buf breaking` proves a new field does not break the schema. It does not
// prove that an operation which sets no new field still serialises to the
// bytes it used to, and that is the property the rollout depends on: a server
// or peer that predates these fields has to keep reading forward traffic
// unchanged.
//
// These golden values were captured on the commit before the fields were added
// and are asserted byte-for-byte here. A proto3 field left at its zero value
// contributes nothing to the encoding, so they must not move. If this test
// fails, either a forward operation started emitting a new field, or something
// changed the encoding of existing ones -- both are rollout hazards and neither
// is caught by `buf breaking`.
var forwardWireGolden = []struct {
	change int
	op     int
	kind   string
	hex    string
}{
	{0, 0, "Set", "0a420a0e1a0c0000000000000000000000001201731a190a12080110011a0c0000000000000000000000aa20052a01762212080110011a0c0000000000000000000000aa"},
	{0, 1, "Set", "0a570a0e1a0c00000000000000000000000012016f1a2e0a12080110021a0c0000000000000000000000aa20082a160a141212080110021a0c0000000000000000000000aa2212080110021a0c0000000000000000000000aa"},
	{0, 2, "Set", "0a570a0e1a0c0000000000000000000000001201611a2e0a12080110031a0c0000000000000000000000aa20092a1612141212080110031a0c0000000000000000000000aa2212080110031a0c0000000000000000000000aa"},
	{0, 3, "Add", "12530a12080110031a0c0000000000000000000000aa120e1a0c0000000000000000000000001a190a12080110041a0c0000000000000000000000aa20052a01782212080110041a0c0000000000000000000000aa"},
	{0, 4, "Add", "12570a12080110031a0c0000000000000000000000aa1212080110041a0c0000000000000000000000aa1a190a12080110051a0c0000000000000000000000aa20052a01792212080110051a0c0000000000000000000000aa"},
	{0, 5, "Set", "0a3f0a0e1a0c0000000000000000000000001201741a160a12080110061a0c0000000000000000000000aa200a2212080110061a0c0000000000000000000000aa"},
	{0, 6, "Edit", "2a500a12080110061a0c0000000000000000000000aa12100a0e1a0c0000000000000000000000001a100a0e1a0c0000000000000000000000002a0268693212080110071a0c0000000000000000000000aa"},
	{0, 7, "Set", "0a450a0e1a0c0000000000000000000000001201631a1c0a12080110081a0c0000000000000000000000aa200b2a04010000002212080110081a0c0000000000000000000000aa"},
	{1, 0, "Move", "1a4c0a12080110031a0c0000000000000000000000aa120e1a0c0000000000000000000000001a12080110051a0c0000000000000000000000aa2212080210011a0c0000000000000000000000aa"},
	{1, 1, "Remove", "22380a0e1a0c0000000000000000000000001212080110011a0c0000000000000000000000aa1a12080210021a0c0000000000000000000000aa"},
}

// forwardWirePackGolden covers ChangePack framing as well, so a new pack-level
// field cannot start appearing on an ordinary push either.
const forwardWirePackGolden = "0a06676f6c64656e1202100222ba050a2a08011801220c0000000000000000000000aa2a160a140a104141414141414141414141414141437110011a440a420a0e1a0c0000000000000000000000001201731a190a12080110011a0c0000000000000000000000aa20052a01762212080110011a0c0000000000000000000000aa1a590a570a0e1a0c00000000000000000000000012016f1a2e0a12080110021a0c0000000000000000000000aa20082a160a141212080110021a0c0000000000000000000000aa2212080110021a0c0000000000000000000000aa1a590a570a0e1a0c0000000000000000000000001201611a2e0a12080110031a0c0000000000000000000000aa20092a1612141212080110031a0c0000000000000000000000aa2212080110031a0c0000000000000000000000aa1a5512530a12080110031a0c0000000000000000000000aa120e1a0c0000000000000000000000001a190a12080110041a0c0000000000000000000000aa20052a01782212080110041a0c0000000000000000000000aa1a5912570a12080110031a0c0000000000000000000000aa1212080110041a0c0000000000000000000000aa1a190a12080110051a0c0000000000000000000000aa20052a01792212080110051a0c0000000000000000000000aa1a410a3f0a0e1a0c0000000000000000000000001201741a160a12080110061a0c0000000000000000000000aa200a2212080110061a0c0000000000000000000000aa1a522a500a12080110061a0c0000000000000000000000aa12100a0e1a0c0000000000000000000000001a100a0e1a0c0000000000000000000000002a0268693212080110071a0c0000000000000000000000aa1a470a450a0e1a0c0000000000000000000000001201631a1c0a12080110081a0c0000000000000000000000aa200b2a04010000002212080110081a0c0000000000000000000000aa22b8010a2a08021802220c0000000000000000000000aa2a160a140a104141414141414141414141414141437110021a4e1a4c0a12080110031a0c0000000000000000000000aa120e1a0c0000000000000000000000001a12080110051a0c0000000000000000000000aa2212080210011a0c0000000000000000000000aa1a3a22380a0e1a0c0000000000000000000000001212080110011a0c0000000000000000000000aa1a12080210021a0c0000000000000000000000aa3a160a140a10414141414141414141414141414143711002"

// newForwardWireDoc builds the fixture the golden values were captured from.
// Changing it invalidates them, which is why it is spelled out rather than
// generated.
func newForwardWireDoc(t *testing.T) *document.Document {
	t.Helper()

	actor, err := time.ActorIDFromHex("0000000000000000000000aa")
	require.NoError(t, err)

	doc := document.New("golden")
	doc.SetActor(actor)

	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("s", "v")
		root.SetNewObject("o")
		arr := root.SetNewArray("a")
		arr.AddString("x", "y")
		root.SetNewText("t").Edit(0, 0, "hi")
		root.SetNewCounter("c", 1)
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		a := root.GetArray("a")
		a.MoveFront(a.Get(1).CreatedAt())
		root.Delete("s")
		return nil
	}))

	return doc
}

func TestForwardOperationsAreByteIdenticalOnTheWire(t *testing.T) {
	pbPack, err := converter.ToChangePack(newForwardWireDoc(t).CreateChangePack())
	require.NoError(t, err)

	var got []string
	for _, ch := range pbPack.Changes {
		for _, op := range ch.Operations {
			b, err := proto.Marshal(op)
			require.NoError(t, err)
			got = append(got, hex.EncodeToString(b))
		}
	}
	require.Len(t, got, len(forwardWireGolden),
		"the fixture produced a different number of operations than the golden values cover")

	for i, want := range forwardWireGolden {
		assert.Equal(t, want.hex, got[i],
			"change %d op %d (%s) changed on the wire", want.change, want.op, want.kind)
	}

	packBytes, err := proto.Marshal(pbPack)
	require.NoError(t, err)
	assert.Equal(t, forwardWirePackGolden, hex.EncodeToString(packBytes),
		"ChangePack framing changed on the wire")
}

// TestForwardOperationsCarryNoRestoreMode is the same property stated
// structurally, so a future golden refresh cannot quietly accept a forward
// operation that has started to advertise a restore.
func TestForwardOperationsCarryNoRestoreMode(t *testing.T) {
	pbPack, err := converter.ToChangePack(newForwardWireDoc(t).CreateChangePack())
	require.NoError(t, err)

	for _, ch := range pbPack.Changes {
		for _, op := range ch.Operations {
			switch b := op.Body.(type) {
			case *api.Operation_Set_:
				assert.Equal(t, api.RestoreMode_RESTORE_MODE_UNSPECIFIED, b.Set.RestoreMode)
			case *api.Operation_Add_:
				assert.Equal(t, api.RestoreMode_RESTORE_MODE_UNSPECIFIED, b.Add.RestoreMode)
			case *api.Operation_ArraySet_:
				assert.Equal(t, api.RestoreMode_RESTORE_MODE_UNSPECIFIED, b.ArraySet.RestoreMode)
			}
		}
	}

	assert.Empty(t, pbPack.Capabilities,
		"a client must not advertise capabilities it has not been taught to honour")
}

// TestServerCapabilitiesContainElementRestore pins the handshake's default-deny
// direction: absence has to read as unsupported, because a server that predates
// a capability cannot report that it lacks it.
func TestServerCapabilitiesContainElementRestore(t *testing.T) {
	assert.True(t, api.HasCapability(api.ServerCapabilities, api.CapElementRestore))
	assert.False(t, api.HasCapability(nil, api.CapElementRestore),
		"an empty list must read as unsupported")
	assert.False(t, api.HasCapability([]string{"something-else"}, api.CapElementRestore))
}
