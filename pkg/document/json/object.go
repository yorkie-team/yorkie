package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Object represents a JSON object, but unlike regular JSON, it has time
// tickets which is created by logical clock.
type Object struct {
	members   *datatype.RHT
	createdAt *time.Ticket
}

// NewObject creates a new instance of Object.
func NewObject(members *datatype.RHT, createdAt *time.Ticket) *Object {
	return &Object{
		members:   members,
		createdAt: createdAt,
	}
}

// Set sets the given element of the given key.
func (o *Object) Set(k string, v datatype.Element) {
	o.members.Set(k, v)
}

// Members returns the member of this object as a map.
func (o *Object) Members() map[string]datatype.Element {
	return o.members.Members()
}

// Marshal returns the JSON encoding of this object.
func (o *Object) Marshal() string {
	return o.members.Marshal()
}

// CreatedAt returns the creation time of this object.
func (o *Object) CreatedAt() *time.Ticket {
	return o.createdAt
}

// Get returns the value of the given key.
func (o *Object) Get(k string) datatype.Element {
	return o.members.Get(k)
}

// RemoveByCreatedAt removes the element of the given creation time.
func (o *Object) RemoveByCreatedAt(createdAt *time.Ticket) datatype.Element {
	return o.members.RemoveByCreatedAt(createdAt)
}

// Remove removes the element of the given key.
func (o *Object) Remove(k string) datatype.Element {
	return o.members.Remove(k)
}
