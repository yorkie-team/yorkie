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

// Package xml provides XML-like document to the CRDT version.
package xml

import "strings"

// DocumentFragment is a fragment of XML.
type DocumentFragment struct {
	children []Node
}

// NewDocumentFragment creates a new instance of DocumentFragment.
func NewDocumentFragment() *DocumentFragment {
	return &DocumentFragment{}
}

// Append appends the given node to the children of this DocumentFragment.
func (f *DocumentFragment) Append(n Node) {
	f.children = append(f.children, n)
}

// String returns the string representation of this DocumentFragment.
func (f *DocumentFragment) String() string {
	sb := strings.Builder{}

	sb.WriteString("<document>")
	for _, child := range f.children {
		sb.WriteString(child.String())
	}
	sb.WriteString("</document>")

	return sb.String()
}

// ChildNodes returns the children of this DocumentFragment.
func (f *DocumentFragment) ChildNodes() []Node {
	return f.children
}

// Delete deletes the given range of children of this DocumentFragment.
func (f *DocumentFragment) Delete(from int, to int) {
	// TODO: implement this
}
