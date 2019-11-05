package proxy

import (
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/json/datatype"
)

func toOriginal(element datatype.Element) datatype.Element {
	switch elem := element.(type) {
	case *ObjectProxy:
		return json.NewObject(datatype.NewRHT(), elem.Object.CreatedAt())
	case *ArrayProxy:
		return json.NewArray(datatype.NewRGA(), elem.Array.CreatedAt())
	case *datatype.Primitive:
		return elem
	}

	return nil
}
