/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package json provides the JSON document implementation.
package json

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/errors"
)

// ErrMidSurrogatePair is returned when an index falls between the two UTF-16
// code units of a surrogate pair. Splitting a node there leaves a lone half on
// each side, and the two SDKs disagree on what a lone half is: Go's strings
// are UTF-8, so utf16.Decode has to substitute U+FFFD, while JS keeps the raw
// code unit. The structure converges either way, but the text does not, so the
// local APIs refuse the index instead of minting an operation whose result
// depends on which SDK applies it. See docs/design/document-editing.md.
var ErrMidSurrogatePair = errors.InvalidArgument("index should not fall in the middle of a surrogate pair")

func toOriginal(elem crdt.Element) crdt.Element {
	switch elem := elem.(type) {
	case *Object:
		return elem.Object
	case *Array:
		return elem.Array
	case *Text:
		return elem.Text
	case *Counter:
		return elem.Counter
	case *Tree:
		return elem.Tree
	case *crdt.Primitive:
		return elem
	}
	panic("unsupported type")
}
