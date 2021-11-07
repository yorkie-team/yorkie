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

package json

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// InitialRichTextNode creates an initial node of RichText. The text is edited
// as this node is split into multiple nodes.
func InitialRichTextNode() *RGATreeSplitNode {
	return NewRGATreeSplitNode(initialNodeID, &RichTextValue{
		attrs: NewRHT(),
		value: "",
	})
}

// RichTextValue is a value of RichText which has an attributes that expresses
// the text style.
type RichTextValue struct {
	attrs *RHT
	value string
}

// NewRichTextValue creates a value of RichText.
func NewRichTextValue(attrs *RHT, value string) *RichTextValue {
	return &RichTextValue{
		attrs: attrs,
		value: value,
	}
}

// Attrs returns the attributes of this value.
func (t *RichTextValue) Attrs() *RHT {
	return t.attrs
}

// Value returns the value of this rich text value.
func (t *RichTextValue) Value() string {
	return t.value
}

// Len returns the length of this value.
// It is calculated in UTF-16 code units.
func (t *RichTextValue) Len() int {
	encoded := utf16.Encode([]rune(t.value))
	return len(encoded)
}

// String returns the string representation of this value.
func (t *RichTextValue) String() string {
	return fmt.Sprintf(`{"attrs":%s,"val":"%s"}`, t.attrs.Marshal(), t.value)
}

// AnnotatedString returns a String containing the metadata of this value
// for debugging purpose.
func (t *RichTextValue) AnnotatedString() string {
	return fmt.Sprintf(`%s "%s"`, t.attrs.Marshal(), t.value)
}

// Split splits this value by the given offset.
func (t *RichTextValue) Split(offset int) RGATreeSplitValue {
	value := t.value
	encoded := utf16.Encode([]rune(value))
	t.value = string(utf16.Decode(encoded[0:offset]))
	return NewRichTextValue(t.attrs.DeepCopy(), string(utf16.Decode(encoded[offset:])))
}

// DeepCopy copies itself deeply.
func (t *RichTextValue) DeepCopy() RGATreeSplitValue {
	return &RichTextValue{
		attrs: t.attrs.DeepCopy(),
		value: t.value,
	}
}

// RichText is an extended data type for the contents of a text editor.
type RichText struct {
	rgaTreeSplit *RGATreeSplit
	selectionMap map[string]*Selection
	createdAt    *time.Ticket
	movedAt      *time.Ticket
	removedAt    *time.Ticket
}

// NewRichText creates a new instance of RichText.
func NewRichText(elements *RGATreeSplit, createdAt *time.Ticket) *RichText {
	return &RichText{
		rgaTreeSplit: elements,
		selectionMap: make(map[string]*Selection),
		createdAt:    createdAt,
	}
}

// NewInitialRichText creates a new instance of RichText.
func NewInitialRichText(elements *RGATreeSplit, createdAt *time.Ticket) *RichText {
	text := NewRichText(elements, createdAt)
	fromPos, toPos := text.CreateRange(0, 0)
	text.Edit(fromPos, toPos, nil, "\n", nil, createdAt)
	return text
}

// Marshal returns the JSON encoding of this rich text.
func (t *RichText) Marshal() string {
	var values []string

	node := t.rgaTreeSplit.initialHead.next
	for node != nil {
		if node.createdAt().Compare(t.createdAt) == 0 {
			// last line
		} else if node.removedAt == nil {
			values = append(values, node.String())
		}
		node = node.next
	}

	return fmt.Sprintf("[%s]", strings.Join(values, ","))
}

// DeepCopy copies itself deeply.
func (t *RichText) DeepCopy() Element {
	rgaTreeSplit := NewRGATreeSplit(InitialRichTextNode())

	current := rgaTreeSplit.InitialHead()
	for _, node := range t.Nodes() {
		current = rgaTreeSplit.InsertAfter(current, node.DeepCopy())
		insPrevID := node.InsPrevID()
		if insPrevID != nil {
			insPrevNode := rgaTreeSplit.FindNode(insPrevID)
			if insPrevNode == nil {
				log.Logger.Warn("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrevNode)
		}
	}

	return NewRichText(rgaTreeSplit, t.createdAt)
}

// CreatedAt returns the creation time of this Text.
func (t *RichText) CreatedAt() *time.Ticket {
	return t.createdAt
}

// RemovedAt returns the removal time of this Text.
func (t *RichText) RemovedAt() *time.Ticket {
	return t.removedAt
}

// MovedAt returns the move time of this Text.
func (t *RichText) MovedAt() *time.Ticket {
	return t.movedAt
}

// SetMovedAt sets the move time of this Text.
func (t *RichText) SetMovedAt(movedAt *time.Ticket) {
	t.movedAt = movedAt
}

// SetRemovedAt sets the removal time of this array.
func (t *RichText) SetRemovedAt(removedAt *time.Ticket) {
	t.removedAt = removedAt
}

// Remove removes this Text.
func (t *RichText) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(t.createdAt)) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

// CreateRange returns a pair of RGATreeSplitNodePos of the given integer offsets.
func (t *RichText) CreateRange(from, to int) (*RGATreeSplitNodePos, *RGATreeSplitNodePos) {
	return t.rgaTreeSplit.createRange(from, to)
}

// Edit edits the given range with the given content and attributes.
func (t *RichText) Edit(
	from,
	to *RGATreeSplitNodePos,
	latestCreatedAtMapByActor map[string]*time.Ticket,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
) (*RGATreeSplitNodePos, map[string]*time.Ticket) {
	val := NewRichTextValue(NewRHT(), content)
	for key, value := range attributes {
		val.attrs.Set(key, value, executedAt)
	}

	cursorPos, latestCreatedAtMapByActor := t.rgaTreeSplit.edit(
		from,
		to,
		latestCreatedAtMapByActor,
		val,
		executedAt,
	)

	if log.Core.Enabled(zap.DebugLevel) {
		log.Logger.Debugf(
			"EDIT: '%s' edits %s",
			executedAt.ActorID().String(),
			t.rgaTreeSplit.AnnotatedString(),
		)
	}

	return cursorPos, latestCreatedAtMapByActor
}

// SetStyle applies the style of the given range.
func (t *RichText) SetStyle(
	from,
	to *RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
) {
	// 01. Split nodes with from and to
	_, toRight := t.rgaTreeSplit.findNodeWithSplit(to, executedAt)
	_, fromRight := t.rgaTreeSplit.findNodeWithSplit(from, executedAt)

	// 02. style nodes between from and to
	nodes := t.rgaTreeSplit.findBetween(fromRight, toRight)
	for _, node := range nodes {
		val := node.value.(*RichTextValue)
		for key, value := range attributes {
			val.attrs.Set(key, value, executedAt)
		}
	}

	if log.Core.Enabled(zap.DebugLevel) {
		log.Logger.Debugf(
			"STYL: '%s' styles %s",
			executedAt.ActorID().String(),
			t.rgaTreeSplit.AnnotatedString(),
		)
	}
}

// Select stores that the given range has been selected.
func (t *RichText) Select(
	from *RGATreeSplitNodePos,
	to *RGATreeSplitNodePos,
	executedAt *time.Ticket,
) {
	if prev, ok := t.selectionMap[executedAt.ActorIDHex()]; !ok || executedAt.After(prev.updatedAt) {
		t.selectionMap[executedAt.ActorIDHex()] = newSelection(from, to, executedAt)

		if log.Core.Enabled(zap.DebugLevel) {
			log.Logger.Debugf(
				"SELT: '%s' selects %s",
				executedAt.ActorID().String(),
				t.rgaTreeSplit.AnnotatedString(),
			)
		}
	}
}

// Nodes returns the internal nodes of this rich text.
func (t *RichText) Nodes() []*RGATreeSplitNode {
	return t.rgaTreeSplit.nodes()
}

// AnnotatedString returns a String containing the metadata of the text
// for debugging purpose.
func (t *RichText) AnnotatedString() string {
	return t.rgaTreeSplit.AnnotatedString()
}

// removedNodesLen returns length of removed nodes
func (t *RichText) removedNodesLen() int {
	return t.rgaTreeSplit.removedNodesLen()
}

// cleanupRemovedNodes cleans up nodes that have been removed.
// The cleaned nodes are subject to garbage collector collection.
func (t *RichText) cleanupRemovedNodes(ticket *time.Ticket) int {
	return t.rgaTreeSplit.cleanupRemovedNodes(ticket)
}
