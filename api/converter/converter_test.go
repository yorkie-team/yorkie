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
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestConverter(t *testing.T) {
	t.Run("snapshot simple test", func(t *testing.T) {
		obj, err := converter.BytesToObject(nil)
		assert.NoError(t, err)
		assert.Equal(t, "{}", obj.Marshal())

		doc := document.New("d1")

		err = doc.Update(func(root *json.Object) error {
			_, _ = root.SetNewText("k1").Edit(0, 0, "A")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"A"}]}`, doc.Marshal())

		err = doc.Update(func(root *json.Object) error {
			_, _ = root.SetNewText("k1").Edit(0, 0, "B")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"B"}]}`, doc.Marshal())

		bytes, err := converter.ObjectToBytes(doc.RootObject())
		assert.NoError(t, err)

		obj, err = converter.BytesToObject(bytes)
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":[{"val":"B"}]}`, obj.Marshal())
	})

	t.Run("snapshot test", func(t *testing.T) {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetNull("k1.0").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			root.SetNewArray("k2").
				AddNull().
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			// plain text
			plainText := root.SetNewText("k3")
			_, _ = plainText.Edit(0, 0, "ㅎ")
			_, _ = plainText.Edit(0, 1, "하")
			_, _ = plainText.Edit(0, 1, "한")
			_, _ = plainText.Edit(0, 1, "하")
			_, _ = plainText.Edit(1, 1, "느")
			_, _ = plainText.Edit(1, 2, "늘")
			_, _ = plainText.Edit(2, 2, "구름")
			_, _ = plainText.Edit(2, 3, "뭉게구")

			// rich text
			richText := root.SetNewText("k4")
			_, _ = richText.Edit(0, 0, "Hello world", nil)
			_, _ = richText.Edit(6, 11, "sky", nil)
			_, _ = richText.Style(0, 5, map[string]string{"b": "1"})

			// a counter
			root.SetNewCounter("k5", crdt.LongCnt, 0).
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
		d1 := document.New("d1")

		err := d1.Update(func(root *json.Object) error {
			// an object and primitive types
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			nextCreatedAt := root.GetArray("k2").Get(0).CreatedAt()
			targetCreatedAt := root.GetArray("k2").Get(1).CreatedAt()
			root.GetArray("k2").MoveBefore(nextCreatedAt, targetCreatedAt)

			// plain text
			plainText := root.SetNewText("k3")
			_, _ = plainText.Edit(0, 0, "ㅎ")
			_, _ = plainText.Edit(0, 1, "하")
			_, _ = plainText.Edit(0, 1, "한")
			_, _ = plainText.Edit(0, 1, "하")
			_, _ = plainText.Edit(1, 1, "느")
			_, _ = plainText.Edit(1, 2, "늘")
			_, _ = plainText.Select(1, 2)

			// rich text
			richText := root.SetNewText("k3")
			_, _ = richText.Edit(0, 0, "Hello World", nil)
			_, _ = richText.Style(0, 5, map[string]string{"b": "1"})

			// counter
			root.SetNewCounter("k4", crdt.IntegerCnt, 0).Increase(5)

			return nil
		})
		assert.NoError(t, err)

		pbPack, err := converter.ToChangePack(d1.CreateChangePack())
		assert.NoError(t, err)

		pack, err := converter.FromChangePack(pbPack)
		assert.NoError(t, err)
		pack.MinSyncedTicket = time.MaxTicket

		d2 := document.New("d1")
		err = d2.ApplyChangePack(pack)
		assert.NoError(t, err)

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("change pack error test", func(t *testing.T) {
		_, err := converter.FromChangePack(nil)
		assert.ErrorIs(t, err, converter.ErrPackRequired)

		_, err = converter.FromChangePack(&api.ChangePack{})
		assert.ErrorIs(t, err, converter.ErrCheckpointRequired)
	})

	t.Run("client test", func(t *testing.T) {
		cli := types.Client{
			ID: time.InitialActorID,
			PresenceInfo: types.PresenceInfo{
				Presence: types.Presence{"Name": "ClientName"},
			},
		}

		pbCli := converter.ToClient(cli)
		decodedCli, err := converter.FromClient(pbCli)
		assert.NoError(t, err)
		assert.Equal(t, cli.ID.Bytes(), decodedCli.ID.Bytes())
		assert.Equal(t, cli.PresenceInfo, decodedCli.PresenceInfo)

		pbClients := converter.ToClients([]types.Client{cli})

		decodedCli, err = converter.FromClient(pbClients[0])
		assert.NoError(t, err)
		assert.Equal(t, cli.ID.Bytes(), decodedCli.ID.Bytes())
		assert.Equal(t, cli.PresenceInfo, decodedCli.PresenceInfo)
	})
}
