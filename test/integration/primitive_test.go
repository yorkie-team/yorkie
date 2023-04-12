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
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPrimitive(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("causal primitive data test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			obj, _ := root.SetNewObject("k1")
			obj.SetBool("k1.1", true)
			obj.SetInteger("k1.2", 2147483647)
			obj.SetLong("k1.3", 9223372036854775807)
			obj.SetDouble("1.4", 1.79)
			obj.SetString("k1.5", "4")
			obj.SetBytes("k1.6", []byte{65, 66})
			obj.SetDate("k1.7", time.Now())

			arr, _ := root.SetNewArray("k2")
			arr.AddBool(true)
			arr.AddInteger(1)
			arr.AddLong(2)
			arr.AddDouble(3.0)
			arr.AddString("4")
			arr.AddBytes([]byte{65})
			arr.AddDate(time.Now())

			return nil
		}, "nested update by c1")
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}
