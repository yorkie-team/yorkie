package datatype

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

// RHT is replicated hash table.
type RHT struct {
	elementQueueMapByKey map[string]*PriorityQueue
	itemMapByCreatedAt   map[string]*PQItem
}

// NewRHT creates a new instance of RHT.
func NewRHT() *RHT {
	return &RHT{
		elementQueueMapByKey: make(map[string]*PriorityQueue),
		itemMapByCreatedAt:   make(map[string]*PQItem),
	}
}

// Get returns the value of the given key.
func (rht *RHT) Get(key string) Element {
	if queue, ok := rht.elementQueueMapByKey[key]; ok {
		item := queue.Peek()
		if item.isRemoved {
			return nil
		}
		return item.value
	}

	return nil
}

// Set sets the value of the given key.
func (rht *RHT) Set(k string, v Element) {
	if _, ok := rht.elementQueueMapByKey[k]; !ok {
		rht.elementQueueMapByKey[k] = NewPriorityQueue()
	}

	item := rht.elementQueueMapByKey[k].Push(v)
	rht.itemMapByCreatedAt[v.CreatedAt().Key()] = item
}

// Remove removes the Element of the given key.
func (rht *RHT) Remove(k string) Element {
	if queue, ok := rht.elementQueueMapByKey[k]; ok {
		item := queue.Peek()
		item.Remove()
		return item.value
	}
	return nil
}

// RemoveByCreatedAt removes the Element of the given creation time.
func (rht *RHT) RemoveByCreatedAt(createdAt *time.Ticket) Element {
	if item, ok := rht.itemMapByCreatedAt[createdAt.Key()]; ok {
		item.Remove()
		return item.value
	}

	log.Logger.Warn("fail to find " + createdAt.Key())
	return nil
}

// Members returns a map of elements because the map easy to use for loop. If we
// encounter performance issues, we need to replace this with other solution.
func (rht *RHT) Members() map[string]Element {
	elementMap := make(map[string]Element)
	for key, queue := range rht.elementQueueMapByKey {
		if item := queue.Peek(); !item.isRemoved {
			elementMap[key] = item.value
		}
	}

	return elementMap
}

// Marshal returns the JSON encoding of this RHT.
func (rht *RHT) Marshal() string {
	members := rht.Members()

	size := len(members)
	keys := make([]string, 0, size)
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	sb := strings.Builder{}
	sb.WriteString("{")

	idx := 0
	for _, k := range keys {
		value := members[k]
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, value.Marshal()))
		if size-1 != idx {
			sb.WriteString(",")
		}
		idx++
	}
	sb.WriteString("}")

	return sb.String()
}
