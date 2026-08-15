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
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
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

func (t *TextValue) DataSize() resource.DataSize {
	dataSize := resource.DataSize{
		Data: len(t.value) * 2,
		Meta: 0,
	}

	for _, node := range t.attrs.Nodes() {
		size := node.DataSize()
		dataSize.Data += size.Data
		dataSize.Meta += size.Meta
	}

	return dataSize
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

// MetaSize returns the size of the metadata of this element.
func (t *Text) MetaSize() int {
	size := 0
	if t.createdAt != nil {
		size += time.TicketSize
	}
	if t.movedAt != nil {
		size += time.TicketSize
	}
	if t.removedAt != nil {
		size += time.TicketSize
	}
	return size
}

// DataSize returns the data usage of this element.
func (t *Text) DataSize() resource.DataSize {
	dataSize := resource.DataSize{
		Data: 0,
		Meta: 0,
	}

	// traverse the nodes and calculate the size
	for _, node := range t.Nodes() {
		if node.createdAt().Compare(t.createdAt) != 0 && node.removedAt == nil {
			size := node.DataSize()
			dataSize.Data += size.Data
			dataSize.Meta += size.Meta
		}
	}

	return resource.DataSize{
		Data: dataSize.Data,
		Meta: dataSize.Meta + t.MetaSize(),
	}
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

	text := NewText(rgaTreeSplit, t.createdAt)
	text.movedAt = t.movedAt
	text.removedAt = t.removedAt
	return text, nil
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

// SetCreatedAt sets the creation time of this Text manually.
func (t *Text) SetCreatedAt(createdAt *time.Ticket) {
	t.createdAt = createdAt
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

// Edit edits the given range with the given content and attributes. Besides
// the caret position, GC pairs, and size diff, it reports the content the
// edit removed: removedValues holds each removed node's text (parallel to
// removedSpans), and removedSpans identifies each by its original identity
// (createdAt, offset range) so a reverse operation can revive or re-remove
// it by that identity rather than by copy-reinserting text.
func (t *Text) Edit(
	from,
	to *RGATreeSplitNodePos,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) (*RGATreeSplitNodePos, []GCPair, resource.DataSize, []string, []RestoreSpan, error) {
	val := NewTextValue(content, NewRHT())
	for key, value := range attributes {
		val.attrs.Set(key, value, executedAt)
	}

	caretPos, pairs, diff, removed, err := t.rgaTreeSplit.edit(
		from,
		to,
		val,
		executedAt,
		versionVector,
	)
	if err != nil {
		return caretPos, pairs, diff, nil, nil, err
	}

	// Pre-size for the common case (server replay, snapshot rebuild) where
	// every removed node is read exactly once below; skip Attrs().Elements()
	// (a fresh map allocation) for the common plain-text node, whose RHT is
	// empty. Text.Restore already treats a nil Attributes map the same as an
	// empty one (its `for k, v := range s.Attributes` is a no-op on nil).
	removedValues := make([]string, 0, len(removed))
	removedSpans := make([]RestoreSpan, 0, len(removed))
	for _, span := range removed {
		content := span.value.String()
		removedValues = append(removedValues, content)

		var attrs map[string]string
		if span.value.Attrs().Len() > 0 {
			attrs = span.value.Attrs().Elements()
		}
		removedSpans = append(removedSpans, RestoreSpan{
			CreatedAt:  span.createdAt,
			Start:      span.start,
			End:        span.end,
			Content:    content,
			Attributes: attrs,
		})
	}

	return caretPos, pairs, diff, removedValues, removedSpans, nil
}

// Restore revives the characters described by spans under their original
// identities. Returns (untombstoned, recreated, stillTombstoned); see
// RGATreeSplit.restore.
func (t *Text) Restore(
	spans []*RestoreSpan,
	executedAt *time.Ticket,
	from *RGATreeSplitNodePos,
) (untombstoned, recreated []*RGATreeSplitNode[*TextValue], stillTombstoned []GCPair) {
	internal := make([]restoreSpanValue[*TextValue], 0, len(spans))
	for _, s := range spans {
		attrs := NewRHT()
		for k, v := range s.Attributes {
			attrs.Set(k, v, executedAt)
		}
		internal = append(internal, restoreSpanValue[*TextValue]{
			createdAt: s.CreatedAt,
			start:     s.Start,
			end:       s.End,
			value:     NewTextValue(s.Content, attrs),
		})
	}
	// `from` is the op's left boundary; it anchors a fragment whose whole
	// insertion was purged (fallback rung in findRestoreAnchor). Identity
	// (createdAt+offset) still addresses every surviving piece first.
	return t.rgaTreeSplit.restore(internal, from, executedAt)
}

// Retombstone re-removes a previously restored range under its original
// identities. Returns GC pairs for the newly tombstoned nodes and the net
// docSize diff from splitting live pieces.
func (t *Text) Retombstone(
	spans []*RestoreSpan,
	executedAt *time.Ticket,
) ([]GCPair, resource.DataSize) {
	internal := make([]restoreSpanValue[*TextValue], 0, len(spans))
	for _, s := range spans {
		internal = append(internal, restoreSpanValue[*TextValue]{
			createdAt: s.CreatedAt,
			start:     s.Start,
			end:       s.End,
			value:     NewTextValue(s.Content, NewRHT()),
		})
	}
	return t.rgaTreeSplit.retombstone(internal, executedAt)
}

// NormalizePos converts the given position into a single absolute offset
// measured from the head of the physical chain. A reverse operation anchors
// on the normalized form so a later split of the chain does not move it.
func (t *Text) NormalizePos(pos *RGATreeSplitNodePos) (*RGATreeSplitNodePos, error) {
	return t.rgaTreeSplit.normalizePos(pos)
}

// RefinePos remaps the given position onto the current split chain, so a
// position recorded before the chain was split still addresses the same place
// in the text.
func (t *Text) RefinePos(pos *RGATreeSplitNodePos) (*RGATreeSplitNodePos, error) {
	return t.rgaTreeSplit.refinePos(pos)
}

// RGATreeSplit returns the underlying RGATreeSplit of this Text.
func (t *Text) RGATreeSplit() *RGATreeSplit[*TextValue] {
	return t.rgaTreeSplit
}

// PrevAttr captures the value a style attribute held immediately before a
// Style or RemoveStyle call overwrote it, or its absence, on the first node
// in the range the call actually visits. A reverse Style uses Existed to
// decide whether to restore Value or remove Key outright.
type PrevAttr struct {
	Key     string
	Value   string
	Existed bool
}

// Style applies the given attributes of the given range. Besides the GC
// pairs and size diff, it reports, for each key in attributes, the value
// that key held (or its absence) on the first node actually styled — see
// PrevAttr — so a reverse Style can restore that prior state. Keys are
// captured in sorted order so the result (and anything built from it, such
// as a reverse operation's wire encoding) is deterministic regardless of
// Go's randomized map iteration order.
func (t *Text) Style(
	from,
	to *RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, []PrevAttr, error) {
	var diff resource.DataSize

	// 01. Split nodes with from and to
	_, toRight, diffTo, err := t.rgaTreeSplit.findNodeWithSplit(to, executedAt)
	if err != nil {
		return t.rgaTreeSplit.drainPendingGCPairs(), diff, nil, err
	}
	_, fromRight, diffFrom, err := t.rgaTreeSplit.findNodeWithSplit(from, executedAt)
	if err != nil {
		diff.Add(diffTo)
		return t.rgaTreeSplit.drainPendingGCPairs(), diff, nil, err
	}

	diff.Add(diffTo, diffFrom)

	// 02. style nodes between from and to
	nodes := t.rgaTreeSplit.findBetween(fromRight, toRight)
	isVersionVectorEmpty := len(versionVector) == 0

	var toBeStyled []*RGATreeSplitNode[*TextValue]

	for _, node := range nodes {
		actorID := node.id.createdAt.ActorID()

		var clientLamportAtChange int64
		if isVersionVectorEmpty {
			// Case 1: local editing from json package
			clientLamportAtChange = time.MaxLamport
		} else {
			// Case 2: from operation with version vector(After v0.5.7)
			lamport, ok := versionVector.Get(actorID)
			if ok {
				clientLamportAtChange = lamport
			} else {
				clientLamportAtChange = 0
			}
		}

		if node.canStyle(executedAt, clientLamportAtChange) {
			toBeStyled = append(toBeStyled, node)
		}
	}

	var pairs []GCPair
	var prevAttrs []PrevAttr
	captured := false
	for _, node := range toBeStyled {
		val := node.value

		if !captured {
			keys := make([]string, 0, len(attributes))
			for key := range attributes {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if val.attrs.Has(key) {
					prevAttrs = append(prevAttrs, PrevAttr{Key: key, Value: val.attrs.Get(key), Existed: true})
				} else {
					prevAttrs = append(prevAttrs, PrevAttr{Key: key, Existed: false})
				}
			}
			captured = true
		}

		for key, value := range attributes {
			if rhtNode := val.attrs.Set(key, value, executedAt); rhtNode != nil {
				pairs = append(pairs, GCPair{
					Parent: node.Value(),
					Child:  rhtNode,
				})
			}
			if newNode, ok := val.attrs.nodeMapByKey[key]; ok {
				diff.Add(newNode.DataSize())
			}
		}
	}

	pairs = append(pairs, t.rgaTreeSplit.drainPendingGCPairs()...)

	return pairs, diff, prevAttrs, nil
}

// RemoveStyle removes the given attributes from the given range. Besides the
// GC pairs and size diff, it reports the value each removed key held on the
// first node actually visited — see PrevAttr — so a reverse operation can
// restore it. Unlike Style, a key that did not exist on that node is simply
// omitted (no Existed: false entry), matching JS's removeStyle: removing an
// already-absent attribute has nothing to reverse. Keys are captured in
// sorted order for the same determinism reason as Style.
func (t *Text) RemoveStyle(
	from,
	to *RGATreeSplitNodePos,
	attributesToRemove []string,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, []PrevAttr, error) {
	var diff resource.DataSize

	// 01. Split nodes with from and to
	_, toRight, diffTo, err := t.rgaTreeSplit.findNodeWithSplit(to, executedAt)
	if err != nil {
		return t.rgaTreeSplit.drainPendingGCPairs(), diff, nil, err
	}
	_, fromRight, diffFrom, err := t.rgaTreeSplit.findNodeWithSplit(from, executedAt)
	if err != nil {
		diff.Add(diffTo)
		return t.rgaTreeSplit.drainPendingGCPairs(), diff, nil, err
	}

	diff.Add(diffTo, diffFrom)

	// 02. find nodes between from and to that can be styled
	nodes := t.rgaTreeSplit.findBetween(fromRight, toRight)
	isVersionVectorEmpty := len(versionVector) == 0

	var toBeStyled []*RGATreeSplitNode[*TextValue]

	for _, node := range nodes {
		actorID := node.id.createdAt.ActorID()

		var clientLamportAtChange int64
		if isVersionVectorEmpty {
			clientLamportAtChange = time.MaxLamport
		} else {
			lamport, ok := versionVector.Get(actorID)
			if ok {
				clientLamportAtChange = lamport
			} else {
				clientLamportAtChange = 0
			}
		}

		if node.canStyle(executedAt, clientLamportAtChange) {
			toBeStyled = append(toBeStyled, node)
		}
	}

	// 03. remove attributes from styled nodes
	var pairs []GCPair
	var prevAttrs []PrevAttr
	captured := false
	for _, node := range toBeStyled {
		val := node.value

		if !captured {
			keys := append([]string(nil), attributesToRemove...)
			sort.Strings(keys)
			for _, key := range keys {
				if val.attrs.Has(key) {
					prevAttrs = append(prevAttrs, PrevAttr{Key: key, Value: val.attrs.Get(key), Existed: true})
				}
			}
			captured = true
		}

		for _, attr := range attributesToRemove {
			rhtNodes := val.attrs.Remove(attr, executedAt)
			for _, rhtNode := range rhtNodes {
				pairs = append(pairs, GCPair{
					Parent: node.Value(),
					Child:  rhtNode,
				})
				diff.Add(rhtNode.DataSize())
			}
		}
	}

	pairs = append(pairs, t.rgaTreeSplit.drainPendingGCPairs()...)

	return pairs, diff, prevAttrs, nil
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
