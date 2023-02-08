/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package xml

import "strings"

// Element is an element of XML.
type Element struct {
	name     string
	children []Node
}

// NewElement creates a new instance of Element.
func NewElement(name string) *Element {
	return &Element{
		name: name,
	}
}

// Append appends the given node to the children of this Element.
func (e *Element) Append(n Node) {
	e.children = append(e.children, n)
}

// String returns the string representation of this Element.
func (e *Element) String() string {
	sb := strings.Builder{}

	sb.WriteString("<")
	sb.WriteString(e.name)
	sb.WriteString(">")

	for _, child := range e.children {
		sb.WriteString(child.String())
	}

	sb.WriteString("</")
	sb.WriteString(e.name)
	sb.WriteString(">")

	return sb.String()
}
