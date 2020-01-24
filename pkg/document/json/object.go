package json

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Object represents a JSON object, but unlike regular JSON, it has time
// tickets which is created by logical clock.
type Object struct {
	memberNodes *RHT
	createdAt   *time.Ticket
}

// NewObject creates a new instance of Object.
func NewObject(memberNodes *RHT, createdAt *time.Ticket) *Object {
	return &Object{
		memberNodes: memberNodes,
		createdAt:   createdAt,
	}
}

// Set sets the given element of the given key.
func (o *Object) Set(k string, v Element) {
	o.memberNodes.Set(k, v, false)
}

// Members returns the member of this object as a map.
func (o *Object) Members() map[string]Element {
	return o.memberNodes.Elements()
}

// CreatedAt returns the creation time of this object.
func (o *Object) CreatedAt() *time.Ticket {
	return o.createdAt
}

// Get returns the value of the given key.
func (o *Object) Get(k string) Element {
	return o.memberNodes.Get(k)
}

// RemoveByCreatedAt removes the element of the given creation time.
func (o *Object) RemoveByCreatedAt(createdAt *time.Ticket) Element {
	return o.memberNodes.RemoveByCreatedAt(createdAt)
}

// Remove removes the element of the given key.
func (o *Object) Remove(k string) Element {
	return o.memberNodes.Remove(k)
}

func (o *Object) Descendants(descendants chan Element) {
	for _, node := range o.memberNodes.AllNodes() {
		switch elem := node.elem.(type) {
		case *Object:
			elem.Descendants(descendants)
		case *Array:
			elem.Descendants(descendants)
		}
		descendants <- node.elem
	}
}
// Marshal returns the JSON encoding of this object.
func (o *Object) Marshal() string {
	members := o.memberNodes.Elements()

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

// Deepcopy copies itself deeply.
func (o *Object) Deepcopy() Element {
	members := NewRHT()

	for _, node := range o.memberNodes.AllNodes() {
		members.Set(node.key, node.elem.Deepcopy(), node.isRemoved)
	}

	return NewObject(members, o.createdAt)
}
