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

package crdt

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/llrb"
	"github.com/yorkie-team/yorkie/pkg/splay"
)

// TextValue is a value of Text which has an attributes that represent
// the text style.
type TextValue struct {
	value string
	attrs *RHT
}

// NewTextValue creates a value of Text.
func NewTextValue(value string, attrs *RHT) *TextValue {
	return &TextValue{
		value: value,
		attrs: attrs,
	}
}

// Attrs returns the attributes of this value.
func (t *TextValue) Attrs() *RHT {
	return t.attrs
}

// Value returns the value of this text value.
func (t *TextValue) Value() string {
	return t.value
}

// Len returns the length of this value.
// It is calculated in UTF-16 code units.
func (t *TextValue) Len() int {
	encoded := utf16.Encode([]rune(t.value))
	return len(encoded)
}

// String returns the string representation of this value.
func (t *TextValue) String() string {
	return t.value
}

// Marshal returns the JSON encoding of this text.
func (t *TextValue) Marshal() string {
	if len(t.attrs.Elements()) == 0 {
		return fmt.Sprintf(`{"val":"%s"}`, EscapeString(t.value))
	}

	return fmt.Sprintf(
		`{"attrs":%s,"val":"%s"}`,
		t.attrs.Marshal(),
		EscapeString(t.value),
	)
}

// toTestString returns a String containing the metadata of this value
// for debugging purpose.
func (t *TextValue) toTestString() string {
	return fmt.Sprintf(
		`%s "%s"`,
		t.attrs.Marshal(),
		EscapeString(t.value),
	)
}

// Split splits this value by the given offset.
func (t *TextValue) Split(offset int) RGATreeSplitValue {
	value := t.value
	encoded := utf16.Encode([]rune(value))
	t.value = string(utf16.Decode(encoded[0:offset]))

	return NewTextValue(
		string(utf16.Decode(encoded[offset:])),
		t.attrs.DeepCopy(),
	)
}

// DeepCopy copies itself deeply.
func (t *TextValue) DeepCopy() RGATreeSplitValue {
	return &TextValue{
		attrs: t.attrs.DeepCopy(),
		value: t.value,
	}
}

// Purge removes the given ticket from this value.
func (t *TextValue) Purge(child GCChild) error {
	rhtNode := child.(*RHTNode)
	return t.attrs.Purge(rhtNode)
}

// GCPairs returns the pairs of GC.
func (t *TextValue) GCPairs() []GCPair {
	if t.attrs == nil {
		return nil
	}

	var pairs []GCPair
	for _, node := range t.attrs.Nodes() {
		if node.isRemoved {
			pairs = append(pairs, GCPair{
				Parent: t,
				Child:  node,
			})
		}
	}

	return pairs
}

// InitialTextNode creates an initial node of Text. The text is edited
// as this node is split into multiple nodes.
func InitialTextNode() *RGATreeSplitNode[*TextValue] {
	return NewRGATreeSplitNode(initialNodeID, &TextValue{
		attrs: NewRHT(),
		value: "",
	})
}

// Text is an extended data type for the contents of a text editor.
type Text struct {
	rgaTreeSplit *RGATreeSplit[*TextValue]
	createdAt    *time.Ticket
	movedAt      *time.Ticket
	removedAt    *time.Ticket
}

// NewText creates a new instance of Text.
func NewText(elements *RGATreeSplit[*TextValue], createdAt *time.Ticket) *Text {
	return &Text{
		rgaTreeSplit: elements,
		createdAt:    createdAt,
	}
}

// String returns the string representation of this Text.
func (t *Text) String() string {
	var values []string

	node := t.rgaTreeSplit.initialHead.next
	for node != nil {
		if node.createdAt().Compare(t.createdAt) != 0 && node.removedAt == nil {
			values = append(values, node.String())
		}
		node = node.next
	}

	return strings.Join(values, "")
}

// Marshal returns the JSON encoding of this Text.
func (t *Text) Marshal() string {
	var values []string

	node := t.rgaTreeSplit.initialHead.next
	for node != nil {
		if node.createdAt().Compare(t.createdAt) != 0 && node.removedAt == nil {
			values = append(values, node.Marshal())
		}
		node = node.next
	}

	return fmt.Sprintf("[%s]", strings.Join(values, ","))
}

// DeepCopy copies itself deeply.
func (t *Text) DeepCopy() (Element, error) {
	rgaTreeSplit := NewRGATreeSplit(InitialTextNode())
	current := rgaTreeSplit.InitialHead()

	for _, node := range t.Nodes() {
		current = rgaTreeSplit.InsertAfter(current, node.DeepCopy())
		insPrevID := node.InsPrevID()
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindNode(insPrevID)
			if insPrevNode == nil {
				return nil, fmt.Errorf("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrevNode)
		}
	}

	return NewText(rgaTreeSplit, t.createdAt), nil
}

// GCPairs returns the pairs of GC.
func (t *Text) GCPairs() []GCPair {
	var pairs []GCPair
	for _, node := range t.Nodes() {
		if node.removedAt != nil {
			pairs = append(pairs, GCPair{
				Parent: t.rgaTreeSplit,
				Child:  node,
			})
		}

		for _, p := range node.Value().GCPairs() {
			pairs = append(pairs, p)
		}
	}

	return pairs
}

// CreatedAt returns the creation time of this Text.
func (t *Text) CreatedAt() *time.Ticket {
	return t.createdAt
}

// RemovedAt returns the removal time of this Text.
func (t *Text) RemovedAt() *time.Ticket {
	return t.removedAt
}

// MovedAt returns the move time of this Text.
func (t *Text) MovedAt() *time.Ticket {
	return t.movedAt
}

// SetMovedAt sets the move time of this Text.
func (t *Text) SetMovedAt(movedAt *time.Ticket) {
	t.movedAt = movedAt
}

// SetRemovedAt sets the removal time of this array.
func (t *Text) SetRemovedAt(removedAt *time.Ticket) {
	t.removedAt = removedAt
}

// Remove removes this Text.
func (t *Text) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(t.createdAt)) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

// CreateRange returns a pair of RGATreeSplitNodePos of the given integer offsets.
func (t *Text) CreateRange(from, to int) (*RGATreeSplitNodePos, *RGATreeSplitNodePos, error) {
	return t.rgaTreeSplit.createRange(from, to)
}

// Edit edits the given range with the given content and attributes.
func (t *Text) Edit(
	from,
	to *RGATreeSplitNodePos,
	maxCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
) (*RGATreeSplitNodePos, map[string]*time.Ticket, []GCPair, error) {
	val := NewTextValue(content, NewRHT())
	for key, value := range attributes {
		val.attrs.Set(key, value, executedAt)
	}

	return t.rgaTreeSplit.edit(
		from,
		to,
		maxCreatedAtMapByActor,
		val,
		executedAt,
	)
}

// Style applies the given attributes of the given range.
func (t *Text) Style(
	from,
	to *RGATreeSplitNodePos,
	maxCreatedAtMapByActor map[string]*time.Ticket,
	attributes map[string]string,
	executedAt *time.Ticket,
) (map[string]*time.Ticket, []GCPair, error) {
	// 01. Split nodes with from and to
	_, toRight, err := t.rgaTreeSplit.findNodeWithSplit(to, executedAt)
	if err != nil {
		return nil, nil, err
	}
	_, fromRight, err := t.rgaTreeSplit.findNodeWithSplit(from, executedAt)
	if err != nil {
		return nil, nil, err
	}

	// 02. style nodes between from and to
	nodes := t.rgaTreeSplit.findBetween(fromRight, toRight)
	createdAtMapByActor := make(map[string]*time.Ticket)
	var toBeStyled []*RGATreeSplitNode[*TextValue]

	for _, node := range nodes {
		actorIDHex := node.id.createdAt.ActorIDHex()

		var maxCreatedAt *time.Ticket
		if len(maxCreatedAtMapByActor) == 0 {
			maxCreatedAt = time.MaxTicket
		} else {
			createdAt, ok := maxCreatedAtMapByActor[actorIDHex]
			if ok {
				maxCreatedAt = createdAt
			} else {
				maxCreatedAt = time.InitialTicket
			}
		}

		if node.canStyle(executedAt, maxCreatedAt) {
			maxCreatedAt = createdAtMapByActor[actorIDHex]
			createdAt := node.id.createdAt
			if maxCreatedAt == nil || createdAt.After(maxCreatedAt) {
				createdAtMapByActor[actorIDHex] = createdAt
			}
			toBeStyled = append(toBeStyled, node)
		}
	}

	var pairs []GCPair
	for _, node := range toBeStyled {
		val := node.value
		for key, value := range attributes {
			if rhtNode := val.attrs.Set(key, value, executedAt); rhtNode != nil {
				pairs = append(pairs, GCPair{
					Parent: node.Value(),
					Child:  rhtNode,
				})
			}
		}
	}

	return createdAtMapByActor, pairs, nil
}

// Nodes returns the internal nodes of this Text.
func (t *Text) Nodes() []*RGATreeSplitNode[*TextValue] {
	return t.rgaTreeSplit.nodes()
}

// ToTestString returns a String containing the metadata of the text
// for debugging purpose.
func (t *Text) ToTestString() string {
	return t.rgaTreeSplit.ToTestString()
}

// CheckWeight returns false when there is an incorrect weight node.
// for debugging purpose.
func (t *Text) CheckWeight() bool {
	return t.rgaTreeSplit.CheckWeight()
}

// TreeByIndex returns IndexTree of the text for debugging purpose.
func (t *Text) TreeByIndex() *splay.Tree[*RGATreeSplitNode[*TextValue]] {
	return t.rgaTreeSplit.treeByIndex
}

// TreeByID returns the tree by ID for debugging purpose.
func (t *Text) TreeByID() *llrb.Tree[*RGATreeSplitNodeID, *RGATreeSplitNode[*TextValue]] {
	return t.rgaTreeSplit.treeByID
}
