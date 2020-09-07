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

package converter_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
)

func TestConverter(t *testing.T) {
	t.Run("snapshot simple test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1").Edit(0, 0, "A")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"A"}`, doc.Marshal())

		err = doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1").Edit(0, 0, "B")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"B"}`, doc.Marshal())

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"B"}`, obj.Marshal())
	})

	t.Run("snapshot test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		err := doc.Update(func(root *proxy.ObjectProxy) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", time.Now())

			// an array
			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(time.Now())

			// plain text
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")

			// rich text
			root.SetNewRichText("k4").
				Edit(0, 0, "Hello world", nil).
				SetStyle(0, 5, map[string]string{"b": "1"})

			// a counter
			root.SetNewCounter("k5", 0).
				Increase(10).
				Increase(math.MaxInt64)

			return nil
		})
		assert.NoError(t, err)

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err := converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, doc.Marshal(), obj.Marshal())
	})

	t.Run("change pack test", func(t *testing.T) {
		d1 := document.New("c1", "d1")

		err := d1.Update(func(root *proxy.ObjectProxy) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", time.Now())

			// an array
			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(time.Now())

			// plain text
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘")

			// plain text
			root.SetNewRichText("k3").
				Edit(0, 0, "Hello World", nil).
				SetStyle(0, 5, map[string]string{"b": "1"})

			return nil
		})
		assert.NoError(t, err)

		pbPack := converter.ToChangePack(d1.CreateChangePack())
		pack, err := converter.FromChangePack(pbPack)
		assert.NoError(t, err)

		d2 := document.New("c1", "d1")
		err = d2.ApplyChangePack(pack)
		assert.NoError(t, err)

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})
}
