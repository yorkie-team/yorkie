package datatype

import (
	"fmt"
	"strings"
)

// RHT is replicated hash table.
type RHT struct {
	members map[string]Element
}

func NewRHT() *RHT {
	return &RHT{
		members: make(map[string]Element),
	}
}

func (rht *RHT) Set(k string, v Element) {
	rht.members[k] = v
}

func (rht *RHT) Members() map[string]Element {
	return rht.members
}

func (rht *RHT) Marshal() string {
	sb := strings.Builder{}

	sb.WriteString("{")

	idx := 0
	for k, v := range rht.members {
		sb.WriteString(fmt.Sprintf("\"%s\":%s", k, v.Marshal()))
		if len(rht.members)-1 != idx {
			sb.WriteString(",")
		}
		idx++
	}

	sb.WriteString("}")

	return sb.String()
}
