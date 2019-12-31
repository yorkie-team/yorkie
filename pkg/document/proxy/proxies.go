package proxy

import (
	"github.com/hackerwins/yorkie/pkg/document/json"
)

func toOriginal(elem json.Element) json.Element {
	switch elem := elem.(type) {
	case *ObjectProxy:
		return json.NewObject(json.NewRHT(), elem.Object.CreatedAt())
	case *ArrayProxy:
		return json.NewArray(json.NewRGA(), elem.Array.CreatedAt())
	case *TextProxy:
		return json.NewText(json.NewRGATreeSplit(), elem.Text.CreatedAt())
	case *json.Primitive:
		return elem
	}

	panic("unsupported type")
}
