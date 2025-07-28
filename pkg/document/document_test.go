/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	errDummy = errors.New("dummy error")
)

func TestDocument(t *testing.T) {
	t.Run("constructor test", func(t *testing.T) {
		doc := document.New("d1")
		assert.Equal(t, doc.Checkpoint(), change.InitialCheckpoint)
		assert.False(t, doc.HasLocalChanges())
		assert.False(t, doc.IsAttached())
	})

	t.Run("status test", func(t *testing.T) {
		doc := document.New("d1")
		assert.False(t, doc.IsAttached())
		doc.SetStatus(document.StatusAttached)
		assert.True(t, doc.IsAttached())
	})

	t.Run("equals test", func(t *testing.T) {
		doc1 := document.New("d1")
		doc2 := document.New("d2")
		doc3 := document.New("d3")

		err := doc1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		}, "updates k1")
		assert.NoError(t, err)

		assert.NotEqual(t, doc1.Marshal(), doc2.Marshal())
		assert.Equal(t, doc2.Marshal(), doc3.Marshal())
	})

	t.Run("nested update test", func(t *testing.T) {
		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`

		doc := document.New("d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5", "v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3")
		assert.NoError(t, err)

		assert.Equal(t, expected, doc.Marshal())
		assert.True(t, doc.HasLocalChanges())
	})

	t.Run("delete test", func(t *testing.T) {
		doc := document.New("d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())
		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5", "v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3")
		assert.NoError(t, err)
		assert.Equal(t, expected, doc.Marshal())

		expected = `{"k1":"v1","k3":["v5","v6"]}`
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k2")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "deletes k2")
		assert.NoError(t, err)
		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("object test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			assert.Equal(t, `{"k1":"v1"}`, root.Marshal())
			root.SetString("k1", "v2")
			assert.Equal(t, `{"k1":"v2"}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v2"}`, doc.Marshal())
	})

	t.Run("array test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddInteger(1).AddInteger(2).AddInteger(3)
			assert.Equal(t, 3, root.GetArray("k1").Len())
			assert.Equal(t, `{"k1":[1,2,3]}`, root.Marshal())
			assert.Equal(t, "[0,0]0[1,1]1[2,1]2[3,1]3", root.GetArray("k1").ToTestString())

			root.GetArray("k1").Delete(1)
			assert.Equal(t, `{"k1":[1,3]}`, root.Marshal())
			assert.Equal(t, 2, root.GetArray("k1").Len())
			assert.Equal(t, "[0,0]0[1,1]1[2,0]2[1,1]3", root.GetArray("k1").ToTestString())

			root.GetArray("k1").InsertIntegerAfter(0, 2)
			assert.Equal(t, `{"k1":[1,2,3]}`, root.Marshal())
			assert.Equal(t, 3, root.GetArray("k1").Len())
			assert.Equal(t, "[0,0]0[1,1]1[3,1]2[1,0]2[1,1]3", root.GetArray("k1").ToTestString())

			root.GetArray("k1").InsertIntegerAfter(2, 4)
			assert.Equal(t, `{"k1":[1,2,3,4]}`, root.Marshal())
			assert.Equal(t, 4, root.GetArray("k1").Len())
			assert.Equal(t, "[0,0]0[1,1]1[2,1]2[2,0]2[3,1]3[4,1]4", root.GetArray("k1").ToTestString())

			for i := range root.GetArray("k1").Len() {
				assert.Equal(
					t,
					fmt.Sprintf("%d", i+1),
					root.GetArray("k1").Get(i).Marshal(),
				)
			}

			return nil
		})

		assert.NoError(t, err)
	})

	t.Run("delete elements of array test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("data").AddInteger(0).AddInteger(1).AddInteger(2)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"data":[0,1,2]}`, doc.Marshal())
		assert.Equal(t, 3, doc.Root().GetArray("data").Len())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("data").Delete(0)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"data":[1,2]}`, doc.Marshal())
		assert.Equal(t, 2, doc.Root().GetArray("data").Len())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("data").Delete(1)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"data":[1]}`, doc.Marshal())
		assert.Equal(t, 1, doc.Root().GetArray("data").Len())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("data").Delete(0)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"data":[]}`, doc.Marshal())
		assert.Equal(t, 0, doc.Root().GetArray("data").Len())
	})

	t.Run("text test", func(t *testing.T) {
		doc := document.New("d1")

		//           ---------- ins links --------
		//           |                |          |
		// [init] - [A] - [12] - [BC deleted] - [D]
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").
				Edit(0, 0, "ABCD").
				Edit(1, 3, "12")
			assert.Equal(t, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			assert.Equal(t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {} "A"][1:3:AA:0 {} "12"]{1:2:AA:1 {} "BC"}[1:2:AA:3 {} "D"]`,
				text.ToTestString(),
			)

			from, _ := text.CreateRange(0, 0)
			assert.Equal(t, "0:0:AA:0:0", from.ToTestString())

			from, _ = text.CreateRange(1, 1)
			assert.Equal(t, "1:2:AA:0:1", from.ToTestString())

			from, _ = text.CreateRange(2, 2)
			assert.Equal(t, "1:3:AA:0:1", from.ToTestString())

			from, _ = text.CreateRange(3, 3)
			assert.Equal(t, "1:3:AA:0:2", from.ToTestString())

			from, _ = text.CreateRange(4, 4)
			assert.Equal(t, "1:2:AA:3:1", from.ToTestString())
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("text composition test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")
			assert.Equal(t, `{"k1":[{"val":"하"},{"val":"늘"}]}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"하"},{"val":"늘"}]}`, doc.Marshal())
	})

	t.Run("rich text test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.SetNewText("k1")
			text.Edit(0, 0, "Hello world", nil)
			assert.Equal(
				t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {} "Hello world"]`,
				text.ToTestString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"Hello world"}]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Style(0, 5, map[string]string{"b": "1"})
			assert.Equal(t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {"b":"1"} "Hello"][1:2:AA:5 {} " world"]`,
				text.ToTestString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hello"},{"val":" world"}]}`,
			doc.Marshal(),
		)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Style(0, 5, map[string]string{"b": "1"})
			assert.Equal(
				t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {"b":"1"} "Hello"][1:2:AA:5 {} " world"]`,
				text.ToTestString(),
			)

			text.Style(3, 5, map[string]string{"i": "1"})
			assert.Equal(
				t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {"b":"1"} "Hel"][1:2:AA:3 {"b":"1","i":"1"} "lo"][1:2:AA:5 {} " world"]`,
				text.ToTestString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" world"}]}`,
			doc.Marshal(),
		)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Edit(5, 11, " Yorkie", nil)
			assert.Equal(
				t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {"b":"1"} "Hel"][1:2:AA:3 {"b":"1","i":"1"} "lo"]`+
					`[4:1:AA:0 {} " Yorkie"]{1:2:AA:5 {} " world"}`,
				text.ToTestString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" Yorkie"}]}`,
			doc.Marshal(),
		)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Edit(5, 5, "\n", map[string]string{"list": "true"})
			assert.Equal(
				t,
				`[0:0:AA:0 {} ""][1:2:AA:0 {"b":"1"} "Hel"][1:2:AA:3 {"b":"1","i":"1"} "lo"]`+
					`[5:1:AA:0 {"list":"true"} "\n"][4:1:AA:0 {} " Yorkie"]{1:2:AA:5 {} " world"}`,
				text.ToTestString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},`+
				`{"attrs":{"list":"true"},"val":"\n"},{"val":" Yorkie"}]}`,
			doc.Marshal(),
		)
	})

	t.Run("counter test", func(t *testing.T) {
		doc := document.New("d1")
		var integer = 10
		var long int64 = 5
		var uinteger uint = 100
		var float float32 = 3.14
		var double = 5.66

		// integer type test
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("age", crdt.IntegerCnt, 5)

			age := root.GetCounter("age")
			age.Increase(long)
			age.Increase(double)
			age.Increase(float)
			age.Increase(uinteger)
			age.Increase(integer)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":128}`, doc.Marshal())
		assert.Equal(t, "128", doc.Root().GetCounter("age").Counter.Marshal())

		// long type test
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("price", crdt.LongCnt, 9000000000000000000)
			price := root.GetCounter("price")
			println(price.ValueType())
			price.Increase(long)
			price.Increase(double)
			price.Increase(float)
			price.Increase(uinteger)
			price.Increase(integer)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":128,"price":9000000000000000123}`, doc.Marshal())

		// negative operator test
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			age := root.GetCounter("age")
			age.Increase(-5)
			age.Increase(-3.14)

			price := root.GetCounter("price")
			price.Increase(-100)
			price.Increase(-20.5)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":120,"price":9000000000000000003}`, doc.Marshal())

		// TODO: it should be modified to error check
		// when 'Remove panic from server code (#50)' is completed.
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			defer func() {
				r := recover()
				assert.NotNil(t, r)
				assert.Equal(t, r, "unsupported type")
			}()

			var notAllowType uint64 = 18300000000000000000
			age := root.GetCounter("age")
			age.Increase(notAllowType)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":120,"price":9000000000000000003}`, doc.Marshal())
	})

	t.Run("rollback test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddInteger(1, 2, 3)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[1,2,3]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddInteger(4, 5)
			return errDummy
		})
		assert.Equal(t, err, errDummy, "should returns the dummy error")
		assert.Equal(t, `{"k1":[1,2,3]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddInteger(4, 5)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[1,2,3,4,5]}`, doc.Marshal())
	})

	t.Run("rollback test, primitive deepcopy", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k1").
				SetInteger("k1.1", 1).
				SetInteger("k1.2", 2)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":{"k1.1":1,"k1.2":2}}`, doc.Marshal())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("k1").Delete("k1.1")
			return errDummy
		})
		assert.Equal(t, err, errDummy, "should returns the dummy error")
		assert.Equal(t, `{"k1":{"k1.1":1,"k1.2":2}}`, doc.Marshal())
	})

	t.Run("text garbage collection test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("text")
			root.GetText("text").Edit(0, 0, "ABCD")
			root.GetText("text").Edit(0, 2, "12")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`[0:0:AA:0 {} ""][1:3:AA:0 {} "12"]{1:2:AA:0 {} "AB"}[1:2:AA:2 {} "CD"]`,
			doc.Root().GetText("text").ToTestString(),
		)

		assert.Equal(t, 1, doc.GarbageLen())
		doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(
			t,
			`[0:0:AA:0 {} ""][1:3:AA:0 {} "12"][1:2:AA:2 {} "CD"]`,
			doc.Root().GetText("text").ToTestString(),
		)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(2, 4, "")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`[0:0:AA:0 {} ""][1:3:AA:0 {} "12"]{1:2:AA:2 {} "CD"}`,
			doc.Root().GetText("text").ToTestString(),
		)
	})

	t.Run("previously inserted elements in heap when running GC test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("a", 1)
			root.SetInteger("a", 2)
			root.Delete("a")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, "{}", doc.Marshal())
		assert.Equal(t, 2, doc.GarbageLen())

		doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
		assert.Equal(t, "{}", doc.Marshal())
		assert.Equal(t, 0, doc.GarbageLen())
	})

	t.Run("version vector test", func(t *testing.T) {
		// 01. create document with actorA and update it twice and check version vector.
		actorA, err := time.ActorIDFromHex("000000000000000000000001")
		assert.NoError(t, err)
		docA := document.New("doc")
		docA.SetActor(actorA)
		assert.Equal(t, "{}", docA.VersionVector().Marshal())

		assert.NoError(t, docA.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "1")
			return nil
		}))
		assert.Equal(t, "{000000000000000000000001:1}", docA.VersionVector().Marshal())

		assert.NoError(t, docA.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "2")
			return nil
		}))
		assert.Equal(t, "{000000000000000000000001:2}", docA.VersionVector().Marshal())

		packA := docA.CreateChangePack()
		assert.True(t, packA.Changes[1].AfterOrEqual(packA.Changes[0]))
		assert.False(t, packA.Changes[0].AfterOrEqual(packA.Changes[1]))

		// 02. create document with actorB and apply change packA of docA to docB and check version vector.
		actorB, err := time.ActorIDFromHex("000000000000000000000002")
		assert.NoError(t, err)
		docB := document.New("doc")
		docB.SetActor(actorB)
		assert.Equal(t, "{}", docB.VersionVector().Marshal())
		// NOTE(JOOHOJANG): Normally, docB's Lamport timestamp should be included in pack.versionVector,
		// because pack is applied after docB is attached.
		// However, since this is not the case in this test method,
		// docB's Lamport timestamp is manually added to packA's versionVector.
		// In actual use, since changePacks cannot be exchanged directly between clients without going through a server,
		// the following handling was added.
		packA.VersionVector.Set(docB.ActorID(), docB.VersionVector().VersionOf(docB.ActorID()))
		assert.NoError(t, docB.ApplyChangePack(packA))
		assert.Equal(t, "{000000000000000000000001:2,000000000000000000000002:3}", docB.VersionVector().Marshal())

		assert.NoError(t, docB.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k2", "3")
			return nil
		}))
		assert.Equal(t, int64(2), docB.VersionVector().VersionOf(actorA))
		assert.Equal(t, int64(4), docB.VersionVector().VersionOf(actorB))
		packB := docB.CreateChangePack()
		assert.True(t, packB.Changes[0].AfterOrEqual(packA.Changes[1]))

		// 03. update docA and docB concurrently and check version vector.
		assert.NoError(t, docA.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "4")
			return nil
		}))
		assert.NoError(t, docB.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k2", "5")
			return nil
		}))
		packA = docA.CreateChangePack()
		packB = docB.CreateChangePack()
		assert.False(t, packA.Changes[2].AfterOrEqual(packB.Changes[1]))
	})
}
