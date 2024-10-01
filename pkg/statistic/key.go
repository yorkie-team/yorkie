package statistic

import "sync/atomic"

type Key uint32

func (tree *Tree[V]) nextKey() Key {
	return Key(atomic.AddUint32(&tree.keyCounter, 1))
}

func (key Key) compare(other Key) int {
	if key > other {
		return 1
	} else if key < other {
		return -1
	}
	return 0
}
