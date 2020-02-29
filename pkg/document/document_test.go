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
	})

	t.Run("equals test", func(t *testing.T) {
		doc1 := document.New("c1", "d1")
		doc2 := document.New("c1", "d2")
		doc3 := document.New("c1", "d3")

		if err := doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "updates k1"); err != nil {
			t.Error(err)
		}

		assert.NotEqual(t, doc1.Marshal(), doc2.Marshal())
		assert.Equal(t, doc2.Marshal(), doc3.Marshal())
	})

	t.Run("nested update test", func(t *testing.T) {
		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`

		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5").AddString("v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3"); err != nil {
			t.Error(err)
		}

		assert.Equal(t, expected, doc.Marshal())
		assert.True(t, doc.HasLocalChanges())
	})

	t.Run("remove test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`
		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5").AddString("v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3"); err != nil {
			t.Error(err)
		}
		assert.Equal(t, expected, doc.Marshal())

		expected = `{"k1":"v1","k3":["v5","v6"]}`
		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k2")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "removes k2"); err != nil {
			t.Error(err)
		}
		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("array test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("1").AddString("2").AddString("3")
			assert.Equal(t, 3, root.GetArray("k1").Len())
			assert.Equal(t, `{"k1":["1","2","3"]}`, root.Marshal())

			root.GetArray("k1").Remove(1)
			assert.Equal(t, `{"k1":["1","3"]}`, root.Marshal())
			assert.Equal(t, 2, root.GetArray("k1").Len())
			return nil
		}); err != nil {
			t.Error(err)
		}
	})

	t.Run("text test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		//           ---------- ins links --------
		//           |                |          |
		// [init] - [A] - [12] - [BC deleted] - [D]
		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1").
				Edit(0, 0, "ABCD").
				Edit(1, 3, "12")
			assert.Equal(t, `{"k1":"A12D"}`, root.Marshal())
			return nil
		}); err != nil {
			t.Error(err)
		}
		assert.Equal(t, `{"k1":"A12D"}`, doc.Marshal())

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
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
		}); err != nil {
			t.Error(err)
		}
	})

	t.Run("text composition test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")
			assert.Equal(t, `{"k1":"하늘"}`, root.Marshal())
			return nil
		}); err != nil {
			t.Error(err)
		}
		assert.Equal(t, `{"k1":"하늘"}`, doc.Marshal())
	})

	t.Run("rollback test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(1).AddInteger(2).AddInteger(3)
			return nil
		}); err != nil {
			t.Error(err)
		}
		assert.Equal(t, `{"k1":[1,2,3]}`, doc.Marshal())

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddInteger(4).AddInteger(5)
			return errDummy
		}); err != errDummy {
			t.Error("should returns the dummy error")
		}
		assert.Equal(t, `{"k1":[1,2,3]}`, doc.Marshal())

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddInteger(4).AddInteger(5)
			return nil
		}); err != nil {
			t.Error(err)
		}
		assert.Equal(t, `{"k1":[1,2,3,4,5]}`, doc.Marshal())
	})
}
