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
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	errDummy          = errors.New("dummy error")
	errDocUpdateDummy = fmt.Errorf("document update: %w", errors.New("dummy error"))
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

		err := doc1.Update(func(root *json.Object) error {
			_, _ = root.SetString("k1", "v1")
			return nil
		}, "updates k1")
		assert.NoError(t, err)

		str1, _ := doc1.Marshal()
		str2, _ := doc2.Marshal()
		str3, _ := doc3.Marshal()
		assert.NotEqual(t, str1, str2)
		assert.Equal(t, str2, str3)
	})

	t.Run("nested update test", func(t *testing.T) {
		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`

		doc := document.New("d1")
		str, _ := doc.Marshal()
		assert.Equal(t, "{}", str)
		assert.False(t, doc.HasLocalChanges())

		err := doc.Update(func(root *json.Object) error {
			_, _ = root.SetString("k1", "v1")
			object, _ := root.SetNewObject("k2")
			_, _ = object.SetString("k4", "v4")
			array, _ := root.SetNewArray("k3")
			_, _ = array.AddString("v5", "v6")
			str, _ := root.Marshal()
			assert.Equal(t, expected, str)
			return nil
		}, "updates k1,k2,k3")
		assert.NoError(t, err)

		str, _ = doc.Marshal()
		assert.Equal(t, expected, str)
		assert.True(t, doc.HasLocalChanges())
	})

	t.Run("delete test", func(t *testing.T) {
		doc := document.New("d1")
		str, _ := doc.Marshal()
		assert.Equal(t, "{}", str)
		assert.False(t, doc.HasLocalChanges())
		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`
		err := doc.Update(func(root *json.Object) error {
			_, _ = root.SetString("k1", "v1")
			object, _ := root.SetNewObject("k2")
			_, _ = object.SetString("k4", "v4")
			array, _ := root.SetNewArray("k3")
			_, _ = array.AddString("v5", "v6")
			str, _ := root.Marshal()
			assert.Equal(t, expected, str)
			return nil
		}, "updates k1,k2,k3")
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, expected, str)

		expected = `{"k1":"v1","k3":["v5","v6"]}`
		err = doc.Update(func(root *json.Object) error {
			_, _ = root.Delete("k2")
			str, _ := root.Marshal()
			assert.Equal(t, expected, str)
			return nil
		}, "deletes k2")
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, expected, str)
	})

	t.Run("object test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object) error {
			_, _ = root.SetString("k1", "v1")
			str, _ := root.Marshal()
			assert.Equal(t, `{"k1":"v1"}`, str)
			_, _ = root.SetString("k1", "v2")
			str, _ = root.Marshal()
			assert.Equal(t, `{"k1":"v2"}`, str)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"k1":"v2"}`, str)
	})

	t.Run("array test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			array, _ := root.SetNewArray("k1")
			_, _ = array.AddInteger(1)
			_, _ = array.AddInteger(2)
			_, _ = array.AddInteger(3)
			array, _ = root.GetArray("k1")
			assert.Equal(t, 3, array.Len())
			str, _ := root.Marshal()
			assert.Equal(t, `{"k1":[1,2,3]}`, str)
			array, _ = root.GetArray("k1")
			str, _ = array.StructureAsString()
			assert.Equal(t, "[0,0]0[1,1]1[2,1]2[3,1]3", str)

			array, _ = root.GetArray("k1")
			_, _ = array.Delete(1)
			str, _ = root.Marshal()
			assert.Equal(t, `{"k1":[1,3]}`, str)
			array, _ = root.GetArray("k1")
			assert.Equal(t, 2, array.Len())
			array, _ = root.GetArray("k1")
			str, _ = array.StructureAsString()
			assert.Equal(t, "[0,0]0[1,1]1[2,0]2[1,1]3", str)

			array, _ = root.GetArray("k1")
			_, _ = array.InsertIntegerAfter(0, 2)
			str, _ = root.Marshal()
			assert.Equal(t, `{"k1":[1,2,3]}`, str)
			array, _ = root.GetArray("k1")
			assert.Equal(t, 3, array.Len())
			array, _ = root.GetArray("k1")
			str, _ = array.StructureAsString()
			assert.Equal(t, "[0,0]0[1,1]1[3,1]2[1,0]2[1,1]3", str)

			array, _ = root.GetArray("k1")
			_, _ = array.InsertIntegerAfter(2, 4)
			str, _ = root.Marshal()
			assert.Equal(t, `{"k1":[1,2,3,4]}`, str)
			array, _ = root.GetArray("k1")
			assert.Equal(t, 4, array.Len())
			array, _ = root.GetArray("k1")
			str, _ = array.StructureAsString()
			assert.Equal(t, "[0,0]0[1,1]1[2,1]2[2,0]2[3,1]3[4,1]4", str)

			array, _ = root.GetArray("k1")
			for i := 0; i < array.Len(); i++ {
				array, _ = root.GetArray("k1")
				elem, _ := array.Get(i)
				str, _ = elem.Marshal()
				assert.Equal(t, fmt.Sprintf("%d", i+1), str)
			}

			return nil
		})

		assert.NoError(t, err)
	})

	t.Run("delete elements of array test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object) error {
			array, _ := root.SetNewArray("data")
			_, _ = array.AddInteger(0)
			_, _ = array.AddInteger(1)
			_, _ = array.AddInteger(2)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"data":[0,1,2]}`, str)
		docRoot, _ := doc.Root()
		array, _ := docRoot.GetArray("data")
		assert.Equal(t, 3, array.Len())

		err = doc.Update(func(root *json.Object) error {
			arr, _ := root.GetArray("data")
			_, _ = arr.Delete(0)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"data":[1,2]}`, str)
		docRoot, _ = doc.Root()
		array, _ = docRoot.GetArray("data")
		assert.Equal(t, 2, array.Len())

		err = doc.Update(func(root *json.Object) error {
			arr, _ := root.GetArray("data")
			_, _ = arr.Delete(1)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"data":[1]}`, str)
		docRoot, _ = doc.Root()
		array, _ = docRoot.GetArray("data")
		assert.Equal(t, 1, array.Len())

		err = doc.Update(func(root *json.Object) error {
			arr, _ := root.GetArray("data")
			_, _ = arr.Delete(0)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"data":[]}`, str)
		docRoot, _ = doc.Root()
		array, _ = docRoot.GetArray("data")
		assert.Equal(t, 0, array.Len())
	})

	t.Run("text test", func(t *testing.T) {
		doc := document.New("d1")

		//           ---------- ins links --------
		//           |                |          |
		// [init] - [A] - [12] - [BC deleted] - [D]
		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k1")
			_, _ = text.Edit(0, 0, "ABCD")
			_, _ = text.Edit(1, 3, "12")
			str, _ := root.Marshal()
			assert.Equal(t, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, str)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, str)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			assert.Equal(t,
				`[0:0:00:0 {} ""][1:2:00:0 {} "A"][1:3:00:0 {} "12"]{1:2:00:1 {} "BC"}[1:2:00:3 {} "D"]`,
				text.StructureAsString(),
			)

			from, _, _ := text.CreateRange(0, 0)
			assert.Equal(t, "0:0:00:0:0", from.StructureAsString())

			from, _, _ = text.CreateRange(1, 1)
			assert.Equal(t, "1:2:00:0:1", from.StructureAsString())

			from, _, _ = text.CreateRange(2, 2)
			assert.Equal(t, "1:3:00:0:1", from.StructureAsString())

			from, _, _ = text.CreateRange(3, 3)
			assert.Equal(t, "1:3:00:0:2", from.StructureAsString())

			from, _, _ = text.CreateRange(4, 4)
			assert.Equal(t, "1:2:00:3:1", from.StructureAsString())
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("text composition test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k1")
			_, _ = text.Edit(0, 0, "ㅎ")
			_, _ = text.Edit(0, 1, "하")
			_, _ = text.Edit(0, 1, "한")
			_, _ = text.Edit(0, 1, "하")
			_, _ = text.Edit(1, 1, "느")
			_, _ = text.Edit(1, 2, "늘")
			str, _ := root.Marshal()
			assert.Equal(t, `{"k1":[{"val":"하"},{"val":"늘"}]}`, str)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"k1":[{"val":"하"},{"val":"늘"}]}`, str)
	})

	t.Run("rich text test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k1")
			_, _ = text.Edit(0, 0, "Hello world", nil)
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {} "Hello world"]`,
				text.StructureAsString(),
			)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"k1":[{"val":"Hello world"}]}`, str)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			_, _ = text.Style(0, 5, map[string]string{"b": "1"})
			assert.Equal(t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"]`,
				text.StructureAsString(),
			)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"k1":[{"attrs":{"b":"1"},"val":"Hello"},{"val":" world"}]}`, str)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			_, _ = text.Style(0, 5, map[string]string{"b": "1"})
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"]`,
				text.StructureAsString(),
			)

			_, _ = text.Style(3, 5, map[string]string{"i": "1"})
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][1:2:00:5 {} " world"]`,
				text.StructureAsString(),
			)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" world"}]}`,
			str,
		)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			_, _ = text.Edit(5, 11, " Yorkie", nil)
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"]`+
					`[4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}`,
				text.StructureAsString(),
			)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" Yorkie"}]}`,
			str,
		)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			_, _ = text.Edit(5, 5, "\n", map[string]string{"list": "true"})
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"]`+
					`[5:1:00:0 {"list":"true"} "\n"][4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}`,
				text.StructureAsString(),
			)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},`+
				`{"attrs":{"list":"true"},"val":"\n"},{"val":" Yorkie"}]}`,
			str,
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
		err := doc.Update(func(root *json.Object) error {
			_, _ = root.SetNewCounter("age", crdt.IntegerCnt, 5)

			age, _ := root.GetCounter("age")
			_, _ = age.Increase(long)
			_, _ = age.Increase(double)
			_, _ = age.Increase(float)
			_, _ = age.Increase(uinteger)
			_, _ = age.Increase(integer)

			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"age":128}`, str)
		docRoot, _ := doc.Root()
		counter, _ := docRoot.GetCounter("age")
		str, _ = counter.Counter.Marshal()
		assert.Equal(t, "128", str)

		// long type test
		err = doc.Update(func(root *json.Object) error {
			_, _ = root.SetNewCounter("price", crdt.LongCnt, 9000000000000000000)
			price, _ := root.GetCounter("price")
			println(price.ValueType())
			_, _ = price.Increase(long)
			_, _ = price.Increase(double)
			_, _ = price.Increase(float)
			_, _ = price.Increase(uinteger)
			_, _ = price.Increase(integer)

			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"age":128,"price":9000000000000000123}`, str)

		// negative operator test
		err = doc.Update(func(root *json.Object) error {
			age, _ := root.GetCounter("age")
			_, _ = age.Increase(-5)
			_, _ = age.Increase(-3.14)

			price, _ := root.GetCounter("price")
			_, _ = price.Increase(-100)
			_, _ = price.Increase(-20.5)

			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"age":120,"price":9000000000000000003}`, str)

		err = doc.Update(func(root *json.Object) error {
			var notAllowType uint64 = 18300000000000000000
			age, _ := root.GetCounter("age")
			_, err = age.Increase(notAllowType)
			assert.Error(t, err, "unsupported type")

			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"age":120,"price":9000000000000000003}`, str)
	})

	t.Run("rollback test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			arr, _ := root.SetNewArray("k1")
			_, _ = arr.AddInteger(1, 2, 3)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"k1":[1,2,3]}`, str)

		err = doc.Update(func(root *json.Object) error {
			arr, _ := root.GetArray("k1")
			_, _ = arr.AddInteger(4, 5)
			return errDummy
		})
		assert.Equal(t, err, errDocUpdateDummy, "should returns the dummy error")
		str, _ = doc.Marshal()
		assert.Equal(t, `{"k1":[1,2,3]}`, str)

		err = doc.Update(func(root *json.Object) error {
			arr, _ := root.GetArray("k1")
			_, _ = arr.AddInteger(4, 5)
			return nil
		})
		assert.NoError(t, err)
		str, _ = doc.Marshal()
		assert.Equal(t, `{"k1":[1,2,3,4,5]}`, str)
	})

	t.Run("rollback test, primitive deepcopy", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			obj, _ := root.SetNewObject("k1")
			_, _ = obj.SetInteger("k1.1", 1)
			_, _ = obj.SetInteger("k1.2", 2)
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, `{"k1":{"k1.1":1,"k1.2":2}}`, str)

		err = doc.Update(func(root *json.Object) error {
			obj, _ := root.GetObject("k1")
			_, _ = obj.Delete("k1.1")
			return errDummy
		})
		assert.Equal(t, err, errDocUpdateDummy, "should returns the dummy error")
		str, _ = doc.Marshal()
		assert.Equal(t, `{"k1":{"k1.1":1,"k1.2":2}}`, str)
	})

	t.Run("text garbage collection test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			_, _ = root.SetNewText("text")
			txt, _ := root.GetText("text")
			_, _ = txt.Edit(0, 0, "ABCD")
			txt, _ = root.GetText("text")
			_, _ = txt.Edit(0, 2, "12")
			return nil
		})
		assert.NoError(t, err)
		docRoot, _ := doc.Root()
		text, _ := docRoot.GetText("text")
		assert.Equal(
			t,
			`[0:0:00:0 {} ""][1:3:00:0 {} "12"]{1:2:00:0 {} "AB"}[1:2:00:2 {} "CD"]`,
			text.StructureAsString(),
		)

		assert.Equal(t, 1, doc.GarbageLen())
		_, _ = doc.GarbageCollect(time.MaxTicket)
		assert.Equal(t, 0, doc.GarbageLen())
		docRoot, _ = doc.Root()
		text, _ = docRoot.GetText("text")
		assert.Equal(
			t,
			`[0:0:00:0 {} ""][1:3:00:0 {} "12"][1:2:00:2 {} "CD"]`,
			text.StructureAsString(),
		)

		err = doc.Update(func(root *json.Object) error {
			txt, _ := root.GetText("text")
			_, _ = txt.Edit(2, 4, "")
			return nil
		})
		assert.NoError(t, err)
		docRoot, _ = doc.Root()
		text, _ = docRoot.GetText("text")
		assert.Equal(
			t,
			`[0:0:00:0 {} ""][1:3:00:0 {} "12"]{1:2:00:2 {} "CD"}`,
			text.StructureAsString(),
		)
	})

	t.Run("previously inserted elements in heap when running GC test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			_, _ = root.SetInteger("a", 1)
			_, _ = root.SetInteger("a", 2)
			_, _ = root.Delete("a")
			return nil
		})
		assert.NoError(t, err)
		str, _ := doc.Marshal()
		assert.Equal(t, "{}", str)
		assert.Equal(t, 2, doc.GarbageLen())

		_, _ = doc.GarbageCollect(time.MaxTicket)
		str, _ = doc.Marshal()
		assert.Equal(t, "{}", str)
		assert.Equal(t, 0, doc.GarbageLen())
	})
}
