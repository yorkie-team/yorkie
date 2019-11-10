package datatype

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Pair struct {
	key string
	element Element
}

func NewPair(key string, element Element) *Pair {
	return &Pair {
		key,
		element,
	}
}

// RHT is replicated hash table.
type RHT struct {
	elementTableByKey       map[string]Element
	elementTableByCreatedAt map[string]*Pair
}

func NewRHT() *RHT {
	return &RHT{
		elementTableByKey:       make(map[string]Element),
		elementTableByCreatedAt: make(map[string]*Pair),
	}
}

func (rht *RHT) Get(k string) Element {
	return rht.elementTableByKey[k]
}

func (rht *RHT) Set(k string, v Element) {
	prev, ok := rht.elementTableByKey[k]
	if !ok || v.CreatedAt().After(prev.CreatedAt()) {
		rht.elementTableByKey[k] = v
		rht.elementTableByCreatedAt[v.CreatedAt().Key()] = NewPair(k, v)
	}
}

func (rht *RHT) RemoveByKey(k string) Element {
	removed, ok := rht.elementTableByKey[k]
	if ok {
		delete(rht.elementTableByKey, k)
	}
	return removed
}

func (rht *RHT) Remove(createdAt *time.Ticket) Element {
	removed, ok := rht.elementTableByCreatedAt[createdAt.Key()]
	if ok {
		delete(rht.elementTableByKey, removed.key)
	}
	return removed.element
}

func (rht *RHT) Members() map[string]Element {
	return rht.elementTableByKey
}

func (rht *RHT) Marshal() string {
	size := len(rht.elementTableByKey)
	keys := make([]string, 0, size)
	for k := range rht.elementTableByKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sb := strings.Builder{}
	sb.WriteString("{")
	idx := 0
	for _, k := range keys {
		elem := rht.elementTableByKey[k]
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, elem.Marshal()))
		if size-1 != idx {
			sb.WriteString(",")
		}
		idx++
	}
	sb.WriteString("}")

	return sb.String()
}
