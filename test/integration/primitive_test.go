// +build integration

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
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPrimitive(t *testing.T) {
	clients := createActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer cleanupClients(t, clients)

	t.Run("causal primitive data test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", time.Now())

			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(time.Now())

			return nil
		}, "nested update by c1")
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}
