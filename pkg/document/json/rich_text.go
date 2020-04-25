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
	updatedAt    *time.Ticket
	removedAt    *time.Ticket
}

// NewText creates a new instance of Text.
func NewRichText(elements *RGATreeSplit, createdAt *time.Ticket) *RichText {
	return &RichText{
		rgaTreeSplit: elements,
		selectionMap: make(map[string]*Selection),
		createdAt:    createdAt,
	}
}

func (t *RichText) Marshal() string {
	var values []string

	node := t.rgaTreeSplit.initialHead.next
	for node != nil {
		if node.removedAt == nil {
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

// UpdatedAt returns the update time of this Text.
func (t *RichText) UpdatedAt() *time.Ticket {
	return t.updatedAt
}

// SetUpdatedAt sets the update time of this Text.
func (t *RichText) SetUpdatedAt(updatedAt *time.Ticket) {
	t.updatedAt = updatedAt
}

// Remove removes this Text.
func (t *RichText) Remove(removedAt *time.Ticket) bool {
	if t.removedAt == nil || removedAt.After(t.removedAt) {
		t.removedAt = removedAt
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
	editedAt *time.Ticket,
) (*RGATreeSplitNodePos, map[string]*time.Ticket) {
	val := NewRichTextValue(NewRHT(), content)
	for key, value := range attributes {
		val.attrs.Set(key, value, editedAt)
	}

	cursorPos, latestCreatedAtMapByActor := t.rgaTreeSplit.edit(
		from,
		to,
		latestCreatedAtMapByActor,
		val,
		editedAt,
	)
	log.Logger.Debugf(
		"EDIT: '%s' edits %s",
		editedAt.ActorID().String(),
		t.rgaTreeSplit.AnnotatedString(),
	)
	return cursorPos, latestCreatedAtMapByActor
}

func (t *RichText) SetStyle(
	from,
	to *RGATreeSplitNodePos,
	attributes map[string]string,
	editedAt *time.Ticket,
) {
	// 01. Split nodes with from and to
	_, toRight := t.rgaTreeSplit.findNodeWithSplit(to, editedAt)
	_, fromRight := t.rgaTreeSplit.findNodeWithSplit(from, editedAt)

	// 02. style nodes between from and to
	nodes := t.rgaTreeSplit.findBetween(fromRight, toRight)

	for _, node := range nodes {
		val := node.value.(*RichTextValue)
		for key, value := range attributes {
			val.attrs.Set(key, value, editedAt)
		}
	}

	log.Logger.Debugf(
		"STYL: '%s' styles %s",
		editedAt.ActorID().String(),
		t.rgaTreeSplit.AnnotatedString(),
	)
}

func (t *RichText) Select(
	from *RGATreeSplitNodePos,
	to *RGATreeSplitNodePos,
	updatedAt *time.Ticket,
) {
	if _, ok := t.selectionMap[updatedAt.ActorIDHex()]; !ok {
		t.selectionMap[updatedAt.ActorIDHex()] = newSelection(from, to, updatedAt)
		return
	}

	prevSelection := t.selectionMap[updatedAt.ActorIDHex()]
	if updatedAt.After(prevSelection.updatedAt) {
		log.Logger.Debugf(
			"SELT: '%s' selects %s",
			updatedAt.ActorID().String(),
			t.rgaTreeSplit.AnnotatedString(),
		)

		t.selectionMap[updatedAt.ActorIDHex()] = newSelection(from, to, updatedAt)
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
