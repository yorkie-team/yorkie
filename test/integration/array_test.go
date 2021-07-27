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

func TestArray(t *testing.T) {
	clients := createActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer cleanupClients(t, clients)

	t.Run("causal nested array test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").
				AddString("v1").
				AddNewArray().AddString("1", "2", "3")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array add/delete simple test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("v1", "v2")
			return nil
		}, "add v1, v2 by c1")
		assert.NoError(t, err)

		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").Delete(1)
			return nil
		}, "delete v2 by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v3")
			return nil
		}, "add v3 by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array add/delete test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("v1")
			return nil
		}, "new array and add v1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v2", "v3")
			root.GetArray("k1").Delete(1)
			return nil
		}, "add v2, v3 and delete v2 by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v4", "v5")
			return nil
		}, "add v4, v5 by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array move test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}, "[0,1,2]")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			prev := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(2)
			root.GetArray("k1").MoveBefore(prev.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[2,0,1]}`, root.Marshal())
			return nil
		}, "move 2 before 0")
		assert.NoError(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			prev := root.GetArray("k1").Get(1)
			elem := root.GetArray("k1").Get(2)
			root.GetArray("k1").MoveBefore(prev.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[0,2,1]}`, root.Marshal())
			return nil
		}, "move 2 before 1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array move with the same position test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			next := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(2)
			root.GetArray("k1").MoveBefore(next.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[2,0,1]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
			next := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(1)
			root.GetArray("k1").MoveBefore(next.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[1,0,2]}`, root.Marshal())
			return nil
		}))

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("array set value", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2, 3, 4, 5, 6, 7, 8)
			root.GetArray("k1").SetNull(0)
			root.GetArray("k1").SetBool(1, true)
			root.GetArray("k1").SetInteger(2, 11)
			root.GetArray("k1").SetLong(3, 12)
			root.GetArray("k1").SetDouble(4, 0.13)
			root.GetArray("k1").SetString(5, "s14")
			root.GetArray("k1").SetBytes(6, []byte("b15"))
			root.GetArray("k1").SetDate(7, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
			root.GetArray("k1").SetNewArray(8)
			assert.Equal(t, `{"k1":[null,true,11,12,0.130000,"s14","b15",2021-01-01T00:00:00Z,[]]}`, root.Marshal())
			return nil
		}))
	})

	t.Run("concurrent array set with the diffrent position", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(0, 3)
			assert.Equal(t, `{"k1":[3,1,2]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(1, 4)
			assert.Equal(t, `{"k1":[0,4,2]}`, root.Marshal())
			return nil
		}))

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[3,4,2]}`, d1.Marshal())
	})

	t.Run("concurrent array set with the same position", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(0, 3)
			assert.Equal(t, `{"k1":[3,1,2]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(0, 4)
			assert.Equal(t, `{"k1":[4,1,2]}`, root.Marshal())
			return nil
		}))
		c1.Sync(ctx)
		c2.Sync(ctx)
		c1.Sync(ctx)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[4,1,2]}`, d1.Marshal())
	})

	t.Run("concurrent array set/insert", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			root.SetNewArray("k2").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2],"k2":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(1, 3)
			root.GetArray("k2").InsertIntegerAfter(0, 4)
			assert.Equal(t, `{"k1":[0,3,2],"k2":[0,4,1,2]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").InsertIntegerAfter(0, 4)
			root.GetArray("k2").SetInteger(1, 3)
			assert.Equal(t, `{"k1":[0,4,1,2],"k2":[0,3,2]}`, root.Marshal())
			return nil
		}))

		c1.Sync(ctx)
		c2.Sync(ctx)
		c1.Sync(ctx)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[0,4,3,2],"k2":[0,4,3,2]}`, d1.Marshal())
	})

	t.Run("concurrent array set/move", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			root.SetNewArray("k2").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2],"k2":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			prev := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(1)
			root.GetArray("k1").MoveBefore(prev.CreatedAt(), elem.CreatedAt())

			root.GetArray("k2").SetInteger(1, 3)
			assert.Equal(t, `{"k1":[1,0,2],"k2":[0,3,2]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(1, 3)

			prev := root.GetArray("k2").Get(0)
			elem := root.GetArray("k2").Get(1)
			root.GetArray("k2").MoveBefore(prev.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[0,3,2],"k2":[1,0,2]}`, root.Marshal())
			return nil
		}))

		c1.Sync(ctx)
		c2.Sync(ctx)
		c1.Sync(ctx)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[3,0,2],"k2":[3,0,2]}`, d1.Marshal())
	})

	t.Run("concurrent array set/delete", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			root.SetNewArray("k2").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2],"k2":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").Delete(1)
			root.GetArray("k2").SetInteger(1, 3)
			assert.Equal(t, `{"k1":[0,2],"k2":[0,3,2]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").SetInteger(1, 3)
			root.GetArray("k2").Delete(1)
			assert.Equal(t, `{"k1":[0,3,2],"k2":[0,2]}`, root.Marshal())
			return nil
		}))

		c1.Sync(ctx)
		c2.Sync(ctx)
		c1.Sync(ctx)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, `{"k1":[0,3,2],"k2":[0,3,2]}`, d1.Marshal())
	})
}
