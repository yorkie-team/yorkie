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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

// fillEverything edits a detached document with every kind of element, so
// every place a ticket can hide is populated before the attach.
func fillEverything(t *testing.T, doc *document.Document) {
	t.Helper()

	require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		text := r.SetNewText("text")
		text.Edit(0, 0, "hello")
		text.Edit(1, 3, "XY")
		text.Style(0, 2, map[string]string{"b": "1"})

		obj := r.SetNewObject("obj")
		obj.SetString("k", "v")
		obj.SetNewArray("arr").AddInteger(1, 2, 3)

		r.SetNewCounter("cnt", 1).Increase(2)

		r.SetNewTree("tree", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "ab"}},
			}},
		})

		r.SetString("gone", "x")
		p.Set("cursor", "1")
		return nil
	}))
	require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.GetTree("tree").Edit(2, 2, &json.TreeNode{Type: "text", Value: "c"}, 0)
		r.GetText("text").Edit(0, 1, "")
		r.Delete("gone")
		arr := r.GetObject("obj").GetArray("arr")
		arr.MoveBefore(arr.Get(0).CreatedAt(), arr.Get(2).CreatedAt())
		return nil
	}))
}

// ticketsOf returns every TimeTicket reachable from the given message,
// decoding the element bytes a Set/Add/ArraySet value carries.
func ticketsOf(t *testing.T, m protoreflect.Message) []*api.TimeTicket {
	t.Helper()

	var tickets []*api.TimeTicket
	switch msg := m.Interface().(type) {
	case *api.TimeTicket:
		return []*api.TimeTicket{msg}
	case *api.JSONElementSimple:
		switch msg.Type {
		case api.ValueType_VALUE_TYPE_JSON_OBJECT,
			api.ValueType_VALUE_TYPE_JSON_ARRAY,
			api.ValueType_VALUE_TYPE_TREE:
			nested := &api.JSONElement{}
			require.NoError(t, proto.Unmarshal(msg.Value, nested))
			tickets = append(tickets, ticketsOf(t, nested.ProtoReflect())...)
		}
	}

	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList():
			if fd.Message() != nil {
				for i := 0; i < v.List().Len(); i++ {
					tickets = append(tickets, ticketsOf(t, v.List().Get(i).Message())...)
				}
			}
		case fd.IsMap():
			if fd.MapValue().Message() != nil {
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					tickets = append(tickets, ticketsOf(t, mv.Message())...)
					return true
				})
			}
		case fd.Message() != nil:
			tickets = append(tickets, ticketsOf(t, v.Message())...)
		}
		return true
	})
	return tickets
}

// actorsOf counts, per actor, the non-initial tickets in the document's root
// and in the change pack it would push.
func actorsOf(t *testing.T, doc *document.Document) map[time.ActorID]int {
	t.Helper()

	internal := doc.InternalDocumentForTest()
	snapshot, err := converter.SnapshotToBytes(internal.RootObject(), nil)
	require.NoError(t, err)
	pbSnapshot := &api.Snapshot{}
	require.NoError(t, proto.Unmarshal(snapshot, pbSnapshot))

	pbPack, err := converter.ToChangePack(doc.CreateChangePack())
	require.NoError(t, err)

	tickets := ticketsOf(t, pbSnapshot.ProtoReflect())
	tickets = append(tickets, ticketsOf(t, pbPack.ProtoReflect())...)

	actors := make(map[time.ActorID]int)
	for _, ticket := range tickets {
		if ticket.Lamport == time.InitialLamport {
			continue
		}
		actor, err := time.ActorIDFromBytes(ticket.ActorId)
		require.NoError(t, err)
		actors[actor]++
	}
	return actors
}

// serverBuild rebuilds a document from the change pack the given document
// would push, the way the server does.
func serverBuild(t *testing.T, docs ...*document.Document) *document.InternalDocument {
	t.Helper()

	built := document.NewInternalDocument(docs[0].Key())
	for _, doc := range docs {
		pbPack, err := converter.ToChangePack(doc.CreateChangePack())
		require.NoError(t, err)
		pack, err := converter.FromChangePack(pbPack)
		require.NoError(t, err)
		_, _, err = built.ApplyChanges(pack.Changes...)
		require.NoError(t, err)
	}
	return built
}

// reissueActor re-issues the document's tickets as Client.Attach does.
func reissueActor(t *testing.T, doc *document.Document, actor time.ActorID) {
	t.Helper()

	require.NoError(t, doc.SetActorWithOptions(actor, document.WithReissue()))
}

// localActorsOf returns the actor of every local change the document would
// push, which is what SetActor rewrites and what the server reads as the
// author of the change.
func localActorsOf(t *testing.T, doc *document.Document) []time.ActorID {
	t.Helper()

	var actors []time.ActorID
	for _, c := range doc.CreateChangePack().Changes {
		actors = append(actors, c.ID().ActorID())
	}
	return actors
}

func rootBytes(t *testing.T, doc *document.InternalDocument) []byte {
	t.Helper()
	b, err := converter.ObjectToBytes(doc.RootObject())
	require.NoError(t, err)
	return b
}

func TestSetActorWithReissue(t *testing.T) {
	actorA, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	actorB, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	t.Run("no ticket keeps the initial actor after attach", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		before := doc.Marshal()
		assert.NotZero(t, actorsOf(t, doc)[time.InitialActorID])

		reissueActor(t, doc, actorA)

		actors := actorsOf(t, doc)
		assert.Zero(t, actors[time.InitialActorID], "%v", actors)
		assert.NotZero(t, actors[actorA])
		assert.Equal(t, before, doc.Marshal())

		vector := doc.VersionVector()
		_, hasInitial := vector.Get(time.InitialActorID)
		assert.False(t, hasInitial, vector.Marshal())
		assert.Equal(t, doc.InternalDocumentForTest().Lamport(), vector.VersionOf(actorA))
		assert.Equal(t, actorA, doc.ActorID())

		internal := doc.InternalDocumentForTest()
		all := internal.AllPresences()
		assert.Contains(t, all, actorA.String())
		assert.NotContains(t, all, time.InitialActorID.String())
	})

	t.Run("local root equals the root the server builds", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		reissueActor(t, doc, actorA)

		built := serverBuild(t, doc)
		assert.Equal(t, doc.Marshal(), built.Marshal())
		assert.True(t, bytes.Equal(rootBytes(t, doc.InternalDocumentForTest()), rootBytes(t, built)))
	})

	t.Run("edits after the re-issue continue under the new actor", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		reissueActor(t, doc, actorA)

		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "Z")
			r.GetTree("tree").Edit(1, 1, &json.TreeNode{Type: "text", Value: "Q"}, 0)
			r.GetObject("obj").SetString("k", "w")
			return nil
		}))
		assert.Zero(t, actorsOf(t, doc)[time.InitialActorID])
		assert.NotZero(t, actorsOf(t, doc)[actorA])

		built := serverBuild(t, doc)
		assert.Equal(t, doc.Marshal(), built.Marshal())
	})

	t.Run("a retried attach re-issues to the next actor", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		reissueActor(t, doc, actorA)
		reissueActor(t, doc, actorB)

		actors := actorsOf(t, doc)
		assert.Zero(t, actors[time.InitialActorID])
		assert.Zero(t, actors[actorA])
		assert.NotZero(t, actors[actorB])
	})

	t.Run("re-issue clears the undo history it invalidates", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		assert.True(t, doc.CanUndo())

		reissueActor(t, doc, actorA)
		assert.False(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
	})

	t.Run("a document that has synced is not re-issued", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		pack := doc.CreateChangePack()
		require.NoError(t, doc.ApplyChangePack(change.NewPack(
			doc.Key(), pack.Checkpoint.NextServerSeq(1), nil, nil, nil,
		)))
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("late", "v")
			return nil
		}))

		reissueActor(t, doc, actorA)
		assert.NotZero(t, actorsOf(t, doc)[time.InitialActorID])

		// The fallback branch is what sets the actor on a document that has
		// already synced -- a re-attach after a detach, say -- so it has to
		// reach the change ID and every buffered local change, exactly as
		// SetActor did before the client switched to WithReissue.
		assert.Equal(t, actorA, doc.ActorID())
		local := localActorsOf(t, doc)
		require.NotEmpty(t, local)
		for _, actor := range local {
			assert.Equal(t, actorA, actor)
		}
	})

	t.Run("a document that absorbed a snapshot is not re-issued", func(t *testing.T) {
		// The checkpoint, the status and the version vector all still look
		// untouched after a snapshot pack carrying the initial checkpoint, so
		// only the absorbed-snapshot guard keeps the rebuild -- which can
		// reproduce the local changes and nothing else -- away from the root.
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)

		snapshot, err := converter.SnapshotToBytes(doc.InternalDocumentForTest().RootObject(), nil)
		require.NoError(t, err)
		pack := change.NewPack(doc.Key(), change.InitialCheckpoint, nil, doc.VersionVector().DeepCopy(), nil)
		pack.Snapshot = snapshot
		require.NoError(t, doc.ApplyChangePack(pack))
		require.True(t, doc.InternalDocumentForTest().HasLocalChanges())
		before := doc.Marshal()

		// A deep copy keeps the guard: the server's snapshot cache hands out
		// copies, and a copy must not look never-synced either.
		copied, err := doc.InternalDocumentForTest().DeepCopy()
		require.NoError(t, err)
		copiedDoc := copied.ToDocument()

		reissueActor(t, doc, actorA)

		assert.Equal(t, before, doc.Marshal())
		assert.NotZero(t, actorsOf(t, doc)[time.InitialActorID])

		reissueActor(t, copiedDoc, actorA)
		assert.NotZero(t, actorsOf(t, copiedDoc)[time.InitialActorID])
	})

	t.Run("without options it is SetActor", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		before, initial := doc.Marshal(), actorsOf(t, doc)[time.InitialActorID]
		require.True(t, doc.CanUndo())

		require.NoError(t, doc.SetActorWithOptions(actorA))

		assert.Equal(t, actorA, doc.ActorID())
		assert.Equal(t, before, doc.Marshal())
		assert.True(t, doc.CanUndo())
		for _, actor := range localActorsOf(t, doc) {
			assert.Equal(t, actorA, actor)
		}
		// The tickets inside the operations and the root keep the initial
		// actor, exactly as SetActor leaves them.
		assert.NotZero(t, actorsOf(t, doc)[time.InitialActorID])
		assert.Less(t, actorsOf(t, doc)[time.InitialActorID], initial)
	})

	t.Run("an empty document only takes the actor", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		reissueActor(t, doc, actorA)
		assert.Equal(t, actorA, doc.ActorID())
		assert.Equal(t, "{}", doc.Marshal())
	})

	t.Run("a Text restored by undo keeps its content", func(t *testing.T) {
		// The wire carries a Text value without its content, so a re-issue
		// that round-trips a restoring Set would empty the Text locally, and
		// the replay of a later Edit on its nodes would fail.
		for _, editAfterUndo := range []bool{false, true} {
			doc := document.New(helper.TestKey(t))
			require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
				r.SetNewText("t").Edit(0, 0, "hello")
				r.SetNewArray("a").AddNewText().Edit(0, 0, "world")
				return nil
			}))
			require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
				r.Delete("t")
				r.GetArray("a").Delete(0)
				return nil
			}))
			require.NoError(t, doc.Undo())
			if editAfterUndo {
				require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
					r.GetText("t").Edit(2, 4, "ZZ")
					r.GetArray("a").GetText(0).Edit(0, 1, "W")
					return nil
				}))
			}
			before := doc.Marshal()

			reissueActor(t, doc, actorA)
			assert.Equal(t, before, doc.Marshal())
			assert.Zero(t, actorsOf(t, doc)[time.InitialActorID])
			assert.NotZero(t, actorsOf(t, doc)[actorA])
		}
	})

	t.Run("tree splits, undo/redo, tree style and array set survive", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewTree("tree", json.TreeNode{Type: "doc", Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
			}}})
			return nil
		}))
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("tree").Edit(3, 3, nil, 1)
			return nil
		}))
		require.NoError(t, doc.Undo())
		require.NoError(t, doc.Redo())
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetTree("tree").Style(0, 1, map[string]string{"a": "b"})
			arr := r.SetNewArray("arr")
			arr.AddInteger(1, 2)
			arr.SetInteger(0, 9)
			r.SetNewCounter("c", 1).Increase(3)
			return nil
		}))
		before := doc.Marshal()
		size, garbage := doc.DocSize(), doc.GarbageLen()

		reissueActor(t, doc, actorA)
		assert.Equal(t, before, doc.Marshal())
		assert.Equal(t, size, doc.DocSize())
		assert.Equal(t, garbage, doc.GarbageLen())
		assert.Zero(t, actorsOf(t, doc)[time.InitialActorID])
		assert.NotZero(t, actorsOf(t, doc)[actorA])
		assert.Equal(t, doc.Marshal(), serverBuild(t, doc).Marshal())
	})

	t.Run("two clients filling the same key before attach converge", func(t *testing.T) {
		key := helper.TestKey(t)
		doc1 := document.New(key)
		doc2 := document.New(key)
		for i, doc := range []*document.Document{doc1, doc2} {
			content := []string{"one", "two"}[i]
			require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
				r.SetNewText("k1").Edit(0, 0, content)
				return nil
			}))
		}
		reissueActor(t, doc1, actorA)
		reissueActor(t, doc2, actorB)

		// The values no longer share a createdAt.
		created1 := doc1.InternalDocumentForTest().RootObject().Get("k1").CreatedAt()
		created2 := doc2.InternalDocumentForTest().RootObject().Get("k1").CreatedAt()
		assert.NotEqual(t, created1.Key(), created2.Key())

		// doc1 reaches the server first; doc2's later Set wins by LWW on the
		// actor tie-break (same lamport, larger actor).
		built := serverBuild(t, doc1, doc2)
		assert.Equal(t, `{"k1":[{"val":"two"}]}`, built.Marshal())

		for _, pair := range [][2]*document.Document{{doc1, doc2}, {doc2, doc1}} {
			pbPack, err := converter.ToChangePack(pair[1].CreateChangePack())
			require.NoError(t, err)
			pack, err := converter.FromChangePack(pbPack)
			require.NoError(t, err)
			_, _, err = pair[0].InternalDocumentForTest().ApplyChanges(pack.Changes...)
			require.NoError(t, err)
		}
		assert.Equal(t, built.Marshal(), doc1.InternalDocumentForTest().Marshal())
		assert.Equal(t, built.Marshal(), doc2.InternalDocumentForTest().Marshal())
	})

	t.Run("an online client entry follows the re-issued actor", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		fillEverything(t, doc)
		doc.InternalDocumentForTest().AddOnlineClient(time.InitialActorID.String())

		reissueActor(t, doc, actorA)

		online := doc.InternalDocumentForTest().Presences()
		assert.Contains(t, online, actorA.String())
		assert.NotContains(t, online, time.InitialActorID.String())
	})
}
