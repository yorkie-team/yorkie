package proxy

import (
	"github.com/yorkie-team/yorkie/pkg/document/json"
)

func toOriginal(elem json.Element) json.Element {
	switch elem := elem.(type) {
	case *ObjectProxy:
		return elem.Object
	case *ArrayProxy:
		return elem.Array
	case *TextProxy:
		return elem.Text
	case *json.Primitive:
		return elem
	}

	panic("unsupported type")
}
