package proxy

import (
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
)

func toOriginal(elem datatype.Element) datatype.Element {
	switch elem := elem.(type) {
	case *ObjectProxy:
		return json.NewObject(datatype.NewRHT(), elem.Object.CreatedAt())
	case *ArrayProxy:
		return json.NewArray(datatype.NewRGA(), elem.Array.CreatedAt())
	case *datatype.Text:
		return elem
	case *datatype.Primitive:
		return elem
	}

	panic("unsupported type")
}
