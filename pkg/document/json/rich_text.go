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
	"unicode/utf8"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
)

func InitialRichTextNode() *RGATreeSplitNode {
	return NewRGATreeSplitNode(initialNodeID, &RichTextValue{
		attrs: NewRHT(),
		value: "",
	})
}

type RichTextValue struct {
	attrs *RHT
	value string
}

func NewRichTextValue(attrs *RHT, value string) *RichTextValue {
	return &RichTextValue{
		attrs: attrs,
		value: value,
	}
}

func (t *RichTextValue) Attrs() *RHT {
	return t.attrs
}

func (t *RichTextValue) Value() string {
	return t.value
}

func (t *RichTextValue) Len() int {
	return utf8.RuneCountInString(t.value)
}

func (t *RichTextValue) String() string {
	return fmt.Sprintf(`{"attrs":%s,"val":"%s"}`, t.attrs.Marshal(), t.value)
}

func (t *RichTextValue) AnnotatedString() string {
	return fmt.Sprintf(`%s "%s"`, t.attrs.Marshal(), t.value)
}

func (t *RichTextValue) Split(offset int) RGATreeSplitValue {
	value := t.value
	r := []rune(value)
	t.value = string(r[0:offset])
	return NewRichTextValue(t.attrs.DeepCopy(), string(r[offset:]))
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

// Remove removes this Text.
func (t *RichText) Remove(executedAt *time.Ticket) bool {
	if t.removedAt == nil || executedAt.After(t.removedAt) {
		t.removedAt = executedAt
		return true
	}
	return false
}

// CreateRange returns pair of RGATreeSplitNodePos of the given integer offsets.
func (t *RichText) CreateRange(from, to int) (*RGATreeSplitNodePos, *RGATreeSplitNodePos) {
	return t.rgaTreeSplit.createRange(from, to)
}

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

	log.Logger.Debugf(
		"EDIT: '%s' edits %s",
		executedAt.ActorID().String(),
		t.rgaTreeSplit.AnnotatedString(),
	)

	return cursorPos, latestCreatedAtMapByActor
}

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

	log.Logger.Debugf(
		"STYL: '%s' styles %s",
		executedAt.ActorID().String(),
		t.rgaTreeSplit.AnnotatedString(),
	)
}

func (t *RichText) Select(
	from *RGATreeSplitNodePos,
	to *RGATreeSplitNodePos,
	executedAt *time.Ticket,
) {
	if prev, ok := t.selectionMap[executedAt.ActorIDHex()]; !ok || executedAt.After(prev.updatedAt) {
		t.selectionMap[executedAt.ActorIDHex()] = newSelection(from, to, executedAt)

		log.Logger.Debugf(
			"SELT: '%s' selects %s",
			executedAt.ActorID().String(),
			t.rgaTreeSplit.AnnotatedString(),
		)
	}
}

func (t *RichText) Nodes() []*RGATreeSplitNode {
	return t.rgaTreeSplit.nodes()
}

// AnnotatedString returns a String containing the meta data of the text
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
