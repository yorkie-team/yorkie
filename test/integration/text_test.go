//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestText(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(0, 0, "ABCD")
			return nil
		}, "edit 0,0 ABCD by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(0, 0, "1234")
			return nil
		}, "edit 0,0 1234 by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(2, 3, "XX")
			return nil
		}, "edit 2,3 XX by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(2, 3, "YY")
			return nil
		}, "edit 2,3 YY by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(4, 5, "ZZ")
			return nil
		}, "edit 4,5 ZZ by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(2, 3, "TT")
			return nil
		}, "edit 2,3 TT by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("rich text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			txt, _ := root.SetNewText("k1")
			txt.Edit(0, 0, "Hello world", nil)
			return nil
		}, `set a new text with "Hello world" by c1`)
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			_, _ = text.Style(0, 1, map[string]string{"b": "1"})
			return nil
		}, `set style b to "H" by c1`)
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			_, _ = text.Style(0, 5, map[string]string{"i": "1"})
			return nil
		}, `set style i to "Hello" by c2`)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent block deletions test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			txt1, _ := root.GetText("k1")
			txt1.Edit(0, 0, "123")
			txt2, _ := root.GetText("k1")
			txt2.Edit(3, 3, "456")
			txt3, _ := root.GetText("k1")
			txt3.Edit(6, 6, "789")
			return nil
		}, "set new text by c1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[{"val":"123"},{"val":"456"},{"val":"789"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(1, 7, "")
			return nil
		}, "delete block by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"1"},{"val":"89"}]}`, d1.Marshal())

		err = d2.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(2, 5, "")
			return nil
		}, "delete block by c2")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"12"},{"val":"6"},{"val":"789"}]}`, d2.Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("new creation then concurrent deletion test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			txt1, _ := root.GetText("k1")
			txt1.Edit(0, 0, "0")
			txt2, _ := root.GetText("k1")
			txt2.Edit(1, 1, "0")
			txt3, _ := root.GetText("k1")
			txt3.Edit(2, 2, "0")
			return nil
		}, "set new text by c1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[{"val":"0"},{"val":"0"},{"val":"0"}]}`, d2.Marshal())

		err = d1.Update(func(root *json.Object) error {
			txt1, _ := root.GetText("k1")
			txt1.Edit(1, 2, "1")
			txt2, _ := root.GetText("k1")
			txt2.Edit(1, 2, "1")
			txt3, _ := root.GetText("k1")
			txt3.Edit(1, 2, "")
			return nil
		}, "newly create then delete by c1")

		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"0"},{"val":"0"}]}`, d1.Marshal())

		err = d2.Update(func(root *json.Object) error {
			txt, _ := root.GetText("k1")
			txt.Edit(0, 3, "")
			return nil
		}, "delete the range includes above new nodes")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[]}`, d2.Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		r1, _ := d1.Root()
		t1, _ := r1.GetText("k1")
		r2, _ := d2.Root()
		t2, _ := r2.GetText("k1")
		assert.True(t, t1.CheckWeight())
		assert.True(t, t2.CheckWeight())
	})
}
