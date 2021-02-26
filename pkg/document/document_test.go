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
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
)

var (
	errDummy = errors.New("dummy error")
)

func TestDocument(t *testing.T) {
	t.Run("constructor test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		assert.Equal(t, doc.Checkpoint(), checkpoint.Initial)
		assert.False(t, doc.HasLocalChanges())
		assert.False(t, doc.IsAttached())
	})

	t.Run("status test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		assert.False(t, doc.IsAttached())
		doc.SetStatus(document.Attached)
		assert.True(t, doc.IsAttached())
	})

	t.Run("equals test", func(t *testing.T) {
		doc1 := document.New("c1", "d1")
		doc2 := document.New("c1", "d2")
		doc3 := document.New("c1", "d3")

		err := doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "updates k1")
		assert.NoError(t, err)

		assert.NotEqual(t, doc1.Marshal(), doc2.Marshal())
		assert.Equal(t, doc2.Marshal(), doc3.Marshal())
	})

	t.Run("nested update test", func(t *testing.T) {
		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`

		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		err := doc.Update(func(root *proxy.ObjectProxy) error {
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
		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`
		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5", "v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3")
		assert.NoError(t, err)
		assert.Equal(t, expected, doc.Marshal())

		expected = `{"k1":"v1","k3":["v5","v6"]}`
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k2")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "deletes k2")
		assert.NoError(t, err)
		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("object test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		err := doc.Update(func(root *proxy.ObjectProxy) error {
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
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(1).AddInteger(2).AddInteger(3)
			assert.Equal(t, 3, root.GetArray("k1").Len())
			assert.Equal(t, `{"k1":[1,2,3]}`, root.Marshal())
			assert.Equal(t, "[0,0]0[1,1]1[2,1]2[3,1]3", root.GetArray("k1").AnnotatedString())

			root.GetArray("k1").Delete(1)
			assert.Equal(t, `{"k1":[1,3]}`, root.Marshal())
			assert.Equal(t, 2, root.GetArray("k1").Len())
			assert.Equal(t, "[0,0]0[1,1]1[2,0]2[1,1]3", root.GetArray("k1").AnnotatedString())

			root.GetArray("k1").InsertIntegerAfter(0, 2)
			assert.Equal(t, `{"k1":[1,2,3]}`, root.Marshal())
			assert.Equal(t, 3, root.GetArray("k1").Len())
			assert.Equal(t, "[0,0]0[1,1]1[3,1]2[1,0]2[1,1]3", root.GetArray("k1").AnnotatedString())

			root.GetArray("k1").InsertIntegerAfter(2, 4)
			assert.Equal(t, `{"k1":[1,2,3,4]}`, root.Marshal())
			assert.Equal(t, 4, root.GetArray("k1").Len())
			assert.Equal(t, "[0,0]0[1,1]1[2,1]2[2,0]2[3,1]3[4,1]4", root.GetArray("k1").AnnotatedString())

			for i := 0; i < root.GetArray("k1").Len(); i++ {
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

	t.Run("text test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		//           ---------- ins links --------
		//           |                |          |
		// [init] - [A] - [12] - [BC deleted] - [D]
		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1").
				Edit(0, 0, "ABCD").
				Edit(1, 3, "12")
			assert.Equal(t, `{"k1":"A12D"}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"A12D"}`, doc.Marshal())

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetText("k1")
			assert.Equal(t,
				"[0:0:00:0 ][1:2:00:0 A][1:3:00:0 12]{1:2:00:1 BC}[1:2:00:3 D]",
				text.AnnotatedString(),
			)

			from, _ := text.CreateRange(0, 0)
			assert.Equal(t, "0:0:00:0:0", from.AnnotatedString())

			from, _ = text.CreateRange(1, 1)
			assert.Equal(t, "1:2:00:0:1", from.AnnotatedString())

			from, _ = text.CreateRange(2, 2)
			assert.Equal(t, "1:3:00:0:1", from.AnnotatedString())

			from, _ = text.CreateRange(3, 3)
			assert.Equal(t, "1:3:00:0:2", from.AnnotatedString())

			from, _ = text.CreateRange(4, 4)
			assert.Equal(t, "1:2:00:3:1", from.AnnotatedString())
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("text composition test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")
			assert.Equal(t, `{"k1":"하늘"}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"하늘"}`, doc.Marshal())
	})

	t.Run("rich text test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.SetNewRichText("k1")
			text.Edit(0, 0, "Hello world", nil)
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {} "Hello world"][1:1:00:0 {} "
"]`,
				text.AnnotatedString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"attrs":{},"val":"Hello world"}]}`, doc.Marshal())

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetRichText("k1")
			text.SetStyle(0, 5, map[string]string{"b": "1"})
			assert.Equal(t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"][1:1:00:0 {} "
"]`,
				text.AnnotatedString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hello"},{"attrs":{},"val":" world"}]}`,
			doc.Marshal(),
		)

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetRichText("k1")
			text.SetStyle(0, 5, map[string]string{"b": "1"})
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"][1:1:00:0 {} "
"]`,
				text.AnnotatedString(),
			)

			text.SetStyle(3, 5, map[string]string{"i": "1"})
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][1:2:00:5 {} " world"][1:1:00:0 {} "
"]`,
				text.AnnotatedString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"attrs":{},"val":" world"}]}`,
			doc.Marshal(),
		)

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetRichText("k1")
			text.Edit(5, 11, " Yorkie", nil)
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"]`+
					`[4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}[1:1:00:0 {} "
"]`,
				text.AnnotatedString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"attrs":{},"val":" Yorkie"}]}`,
			doc.Marshal(),
		)

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetRichText("k1")
			text.Edit(5, 5, "\n", map[string]string{"list": "true"})
			assert.Equal(
				t,
				`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][5:1:00:0 {"list":"true"} "
"][4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}[1:1:00:0 {} "
"]`,
				text.AnnotatedString(),
			)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(
			t,
			`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"attrs":{"list":"true"},"val":"
"},{"attrs":{},"val":" Yorkie"}]}`,
			doc.Marshal(),
		)
	})

	t.Run("counter test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		var integer int = 10
		var long int64 = 5
		var uinteger uint = 100
		var float float32 = 3.14
		var double float64 = 5.66

		// integer type test
		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewCounter("age", 5)

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

		// long type test
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewCounter("price", 9000000000000000000)
			price := root.GetCounter("price")
			price.Increase(long)
			price.Increase(double)
			price.Increase(float)
			price.Increase(uinteger)
			price.Increase(integer)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":128,"price":9000000000000000123}`, doc.Marshal())

		// double type test
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewCounter("width", 10.5)
			width := root.GetCounter("width")
			width.Increase(long)
			width.Increase(double)
			width.Increase(float)
			width.Increase(uinteger)
			width.Increase(integer)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":128,"price":9000000000000000123,"width":134.300000}`, doc.Marshal())

		// negative operator test
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			age := root.GetCounter("age")
			age.Increase(-5)
			age.Increase(-3.14)

			price := root.GetCounter("price")
			price.Increase(-100)
			price.Increase(-20.5)

			width := root.GetCounter("width")
			width.Increase(-4)
			width.Increase(-0.3)

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"age":120,"price":9000000000000000003,"width":130.000000}`, doc.Marshal())

		// TODO: it should be modified to error check
		// when 'Remove panic from server code (#50)' is completed.
		err = doc.Update(func(root *proxy.ObjectProxy) error {
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
		assert.Equal(t, `{"age":120,"price":9000000000000000003,"width":130.000000}`, doc.Marshal())
	})

	t.Run("rollback test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(1, 2, 3)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[1,2,3]}`, doc.Marshal())

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddInteger(4, 5)
			return errDummy
		})
		assert.Equal(t, err, errDummy, "should returns the dummy error")
		assert.Equal(t, `{"k1":[1,2,3]}`, doc.Marshal())

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddInteger(4, 5)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[1,2,3,4,5]}`, doc.Marshal())
	})

	t.Run("rollback test, primitive deepcopy", func(t *testing.T) {
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").
				SetInteger("k1.1", 1).
				SetInteger("k1.2", 2)
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":{"k1.1":1,"k1.2":2}}`, doc.Marshal())

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.GetObject("k1").Delete("k1.1")
			return errDummy
		})
		assert.Equal(t, err, errDummy, "should returns the dummy error")
		assert.Equal(t, `{"k1":{"k1.1":1,"k1.2":2}}`, doc.Marshal())
	})
}
