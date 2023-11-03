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

package llrb_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/llrb"
)

type intKey struct {
	key int
}

func newIntKey(key int) *intKey {
	return &intKey{
		key: key,
	}
}

func (k *intKey) Compare(other llrb.Key) int {
	o := other.(*intKey)
	if k.key > o.key {
		return 1
	} else if k.key < o.key {
		return -1
	}

	return 0
}

type intValue struct {
	value int
}

func newIntValue(value int) *intValue {
	return &intValue{value: value}
}

func (v *intValue) String() string {
	return fmt.Sprintf("%d", v.value)
}

func TestTree(t *testing.T) {
	t.Run("keeping order test", func(t *testing.T) {
		arrays := [][]int{
			{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
			{8, 5, 7, 9, 1, 3, 6, 0, 4, 2},
			{7, 2, 0, 3, 1, 9, 8, 4, 6, 5},
			{2, 0, 3, 5, 8, 6, 4, 1, 9, 7},
			{8, 4, 7, 9, 2, 6, 0, 3, 1, 5},
			{7, 1, 5, 2, 8, 6, 3, 4, 0, 9},
			{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
		}

		for _, array := range arrays {
			tree := llrb.NewTree[*intKey, *intValue]()
			for _, value := range array {
				tree.Put(newIntKey(value), newIntValue(value))
			}
			assert.Equal(t, "0,1,2,3,4,5,6,7,8,9", tree.String())

			tree.Remove(newIntKey(8))
			assert.Equal(t, "0,1,2,3,4,5,6,7,9", tree.String())

			tree.Remove(newIntKey(2))
			assert.Equal(t, "0,1,3,4,5,6,7,9", tree.String())

			tree.Remove(newIntKey(5))
			assert.Equal(t, "0,1,3,4,6,7,9", tree.String())
		}
	})
}
