package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Object struct {
	members   *datatype.RHT
	createdAt *time.Ticket
}

func NewObject(members *datatype.RHT, createdAt *time.Ticket) *Object {
	return &Object{
		members:   members,
		createdAt: createdAt,
	}
}

func (o *Object) Set(k string, v datatype.Element) {
	o.members.Set(k, v)
}

func (o *Object) Members() map[string]datatype.Element {
	return o.members.Members()
}

func (o *Object) Marshal() string {
	return o.members.Marshal()
}

func (o *Object) CreatedAt() *time.Ticket {
	return o.createdAt
}

func (o *Object) Get(k string) datatype.Element {
	return o.members.Get(k)
}

func (o *Object) Remove(createdAt *time.Ticket) datatype.Element {
	return o.members.Remove(createdAt)
}

func (o *Object) RemoveByKey(k string) datatype.Element {
	return o.members.RemoveByKey(k)
}
