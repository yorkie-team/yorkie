//go:build stress
// +build stress

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

package stress

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocumentStress(t *testing.T) {
	t.Run("garbage collection test", func(t *testing.T) {
		size := 10_000

		// 01. initial
		doc := document.New("c1", "d1")
		fmt.Println("-----Initial")
		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)
		helper.PrintSnapshotBytesSize(bytes)
		helper.PrintMemStats()

		// 02. 10,000 integers
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("1")
			for i := 0; i < size; i++ {
				root.GetArray("1").AddInteger(i)
			}

			return nil
		}, "sets big array")
		assert.NoError(t, err)

		// 03. deletes integers
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("1")
			return nil
		}, "deletes the array")
		assert.NoError(t, err)
		fmt.Println("-----Integers")
		bytes, err = converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)
		helper.PrintSnapshotBytesSize(bytes)
		helper.PrintMemStats()

		// 04. after garbage collection
		assert.Equal(t, size+1, doc.GarbageCollect(time.MaxTicket))
		fmt.Println("-----After garbage collection")
		bytes, err = converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)
		helper.PrintSnapshotBytesSize(bytes)
		helper.PrintMemStats()
	})

	t.Run("garbage collection for large size of text garbage test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		printMemStats := func(root *json.Object) {
			bytes, err := converter.ObjectToBytes(doc.RootObject())
			assert.NoError(t, err)
			helper.PrintSnapshotBytesSize(bytes)
			helper.PrintMemStats()
		}

		textSize := 1_000
		// 01. initial
		err := doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.SetNewText("k1")
			for i := 0; i < textSize; i++ {
				text.Edit(i, i, "a")
			}
			return nil
		}, "initial")
		assert.NoError(t, err)
		fmt.Println("-----initial")
		printMemStats(doc.RootObject())

		// 02. 1000 nodes modified
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetText("k1")
			for i := 0; i < textSize; i++ {
				text.Edit(i, i+1, "b")
			}
			return nil
		}, "1000 nodes modified")
		assert.NoError(t, err)
		fmt.Println("-----1000 nodes modified")
		printMemStats(doc.RootObject())
		assert.Equal(t, textSize, doc.GarbageLen())

		// 03. GC
		assert.Equal(t, textSize, doc.GarbageCollect(time.MaxTicket))
		runtime.GC()
		fmt.Println("-----Garbage collect")
		printMemStats(doc.RootObject())

		// 04. long text by one node
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.SetNewText("k2")
			str := ""
			for i := 0; i < textSize; i++ {
				str += "a"
			}
			text.Edit(0, 0, str)
			return nil
		}, "initial")
		fmt.Println("-----long text by one node")
		assert.NoError(t, err)
		printMemStats(doc.RootObject())

		// 05. Modify one node multiple times
		err = doc.Update(func(root *proxy.ObjectProxy) error {
			text := root.GetText("k2")
			for i := 0; i < textSize; i++ {
				if i != textSize {
					text.Edit(i, i+1, "b")
				}
			}
			return nil
		}, "Modify one node multiple times")
		assert.NoError(t, err)
		fmt.Println("-----Modify one node multiple times")
		printMemStats(doc.RootObject())

		// 06. GC
		assert.Equal(t, textSize, doc.GarbageLen())
		assert.Equal(t, textSize, doc.GarbageCollect(time.MaxTicket))
		runtime.GC()
		fmt.Println("-----Garbage collect")
		printMemStats(doc.RootObject())
	})
}
