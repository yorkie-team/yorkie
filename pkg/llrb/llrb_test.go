package llrb_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/hackerwins/yorkie/pkg/llrb"
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
	} else {
		return 0
	}
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

func rangeArray(min, max int) []int {
	a := make([]int, max-min+1)
	for i := range a {
		a[i] = min + i
	}
	return a
}

func shuffle(a []int) []int {
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(a), func(i, j int) { a[i], a[j] = a[j], a[i] })
	return a
}

func TestTree(t *testing.T) {
	t.Run("keeping order test", func(t *testing.T) {
		tree := llrb.NewTree()

		for _, value := range shuffle(rangeArray(0, 9)) {
			tree.Put(newIntKey(value), newIntValue(value))
		}
		assert.Equal(t, "0,1,2,3,4,5,6,7,8,9", tree.String())

		tree.Remove(newIntKey(8))
		assert.Equal(t, "0,1,2,3,4,5,6,7,9", tree.String())

		tree.Remove(newIntKey(2))
		assert.Equal(t, "0,1,3,4,5,6,7,9", tree.String())

		tree.Remove(newIntKey(5))
		assert.Equal(t, "0,1,3,4,6,7,9", tree.String())
	})
}
