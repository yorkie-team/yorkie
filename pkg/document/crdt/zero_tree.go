/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

// PROTOTYPE -- ZeroTree, the server half of the Zero editor's CRDT (see the
// JS SDK's crdt/zero_tree.ts). The document is ONE RGATreeSplit of tokens:
// characters and open markers (type, depth, LWW attributes). The server only
// stores and replays it: token insertion and deletion, marker attribute
// writes, identity-based undo/redo with the removal ledger, GC and
// snapshots. The editor model (buildView) and the event compiler are client
// only; Marshal shows the tokens.

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ZeroAttr is one LWW attribute register of a marker. Removed means the
// value is null; the register is kept so an older write cannot bring it back.
type ZeroAttr struct {
	Key       string
	Value     string
	Removed   bool
	UpdatedAt *time.Ticket
}

// ZeroMarker is the open marker of a Zero element node. Attrs keep the
// order they were first written in.
type ZeroMarker struct {
	Type     string
	Depth    int
	Attrs    []*ZeroAttr
	Boundary string
}

// Set writes one attribute with last-writer-wins. It returns the register
// before the write (nil: none) and whether the write applied.
func (m *ZeroMarker) Set(key, value string, removed bool, ticket *time.Ticket) (*ZeroAttr, bool) {
	for i, attr := range m.Attrs {
		if attr.Key != key {
			continue
		}
		if !ticket.After(attr.UpdatedAt) {
			return attr, false
		}
		m.Attrs[i] = &ZeroAttr{Key: key, Value: value, Removed: removed, UpdatedAt: ticket}
		return attr, true
	}
	m.Attrs = append(m.Attrs, &ZeroAttr{Key: key, Value: value, Removed: removed, UpdatedAt: ticket})
	return nil, true
}

// DeepCopy copies this marker.
func (m *ZeroMarker) DeepCopy() *ZeroMarker {
	attrs := make([]*ZeroAttr, len(m.Attrs))
	for i, attr := range m.Attrs {
		cp := *attr
		attrs[i] = &cp
	}
	return &ZeroMarker{Type: m.Type, Depth: m.Depth, Attrs: attrs, Boundary: m.Boundary}
}

// ZeroToken is a character (Marker nil) or a marker. Ghost marks a
// character a ghost purge dropped; a restore refills it from its span.
type ZeroToken struct {
	Char   rune
	Marker *ZeroMarker
	Ghost  bool
}

// ZeroTokens is the value of an RGATreeSplitNode of a ZeroTree: a run of
// tokens. Scope is the id of the scoped marker the run was inserted in.
type ZeroTokens struct {
	Tokens []ZeroToken
	Scope  string
}

// NewZeroTokens creates a run of tokens.
func NewZeroTokens(tokens []ZeroToken, scope string) *ZeroTokens {
	return &ZeroTokens{Tokens: tokens, Scope: scope}
}

func copyTokens(tokens []ZeroToken) []ZeroToken {
	cp := make([]ZeroToken, len(tokens))
	for i, t := range tokens {
		cp[i] = t
		if t.Marker != nil {
			cp[i].Marker = t.Marker.DeepCopy()
		}
	}
	return cp
}

// Split keeps [0, offset) and returns [offset, len).
func (z *ZeroTokens) Split(offset int) RGATreeSplitValue {
	right := &ZeroTokens{Tokens: copyTokens(z.Tokens[offset:]), Scope: z.Scope}
	z.Tokens = z.Tokens[:offset:offset]
	return right
}

// toGhost drops the characters (GC). Markers stay: their attributes are LWW
// registers a concurrent write may still have changed.
func (z *ZeroTokens) toGhost() {
	for i, t := range z.Tokens {
		if t.Marker == nil {
			z.Tokens[i] = ZeroToken{Ghost: true}
		}
	}
}

// refill puts back the characters of a ghost from a restore span value.
func (z *ZeroTokens) refill(from RGATreeSplitValue, offset int) {
	source := from.(*ZeroTokens).Tokens
	for i, t := range z.Tokens {
		if !t.Ghost || offset+i >= len(source) {
			continue
		}
		if s := source[offset+i]; s.Marker == nil && !s.Ghost {
			z.Tokens[i] = s
		}
	}
}

// Len returns the number of tokens.
func (z *ZeroTokens) Len() int {
	return len(z.Tokens)
}

// DeepCopy copies this run.
func (z *ZeroTokens) DeepCopy() RGATreeSplitValue {
	return &ZeroTokens{Tokens: copyTokens(z.Tokens), Scope: z.Scope}
}

// String shows markers as <type:depth> between the characters.
func (z *ZeroTokens) String() string {
	var b strings.Builder
	for _, t := range z.Tokens {
		if t.Marker == nil {
			b.WriteRune(t.Char)
		} else {
			fmt.Fprintf(&b, "<%s:%d>", t.Marker.Type, t.Marker.Depth)
		}
	}
	return b.String()
}

// Marshal returns the JSON encoding of the tokens.
func (z *ZeroTokens) Marshal() string {
	parts := make([]string, 0, len(z.Tokens))
	for _, t := range z.Tokens {
		if t.Marker == nil {
			parts = append(parts, fmt.Sprintf(`"%s"`, EscapeString(string(t.Char))))
			continue
		}
		var attrs []string
		for _, attr := range t.Marker.Attrs {
			if !attr.Removed {
				attrs = append(attrs, fmt.Sprintf(`"%s":"%s"`, EscapeString(attr.Key), EscapeString(attr.Value)))
			}
		}
		parts = append(parts, fmt.Sprintf(`{"type":"%s","depth":%d,"attrs":{%s}}`,
			EscapeString(t.Marker.Type), t.Marker.Depth, strings.Join(attrs, ",")))
	}
	return strings.Join(parts, ",")
}

func (z *ZeroTokens) toTestString() string {
	return z.String()
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// DataSize mirrors ZeroTokens.getDataSize of the JS SDK.
func (z *ZeroTokens) DataSize() resource.DataSize {
	size := resource.DataSize{}
	for _, t := range z.Tokens {
		if t.Marker == nil {
			size.Data += 2
			continue
		}
		size.Data += utf16Len(t.Marker.Type)*2 + 4
		for _, attr := range t.Marker.Attrs {
			size.Data += (utf16Len(attr.Key) + utf16Len(attr.Value)) * 2
			size.Meta += time.TicketSize
		}
	}
	return size
}

// ZeroRestoreSpan is a token run of one insertion [Start, End) for
// identity-preserving undo/redo. RemovedBy is the deletion it undoes.
type ZeroRestoreSpan struct {
	CreatedAt *time.Ticket
	Start     int
	End       int
	Value     *ZeroTokens
	RemovedBy *time.Ticket
}

// ZeroAttrWrite is one attribute write of ZeroTreeStyle.
type ZeroAttrWrite struct {
	Key     string
	Value   string
	Removed bool
}

// InitialZeroTreeNode creates the head node of a ZeroTree.
func InitialZeroTreeNode() *RGATreeSplitNode[*ZeroTokens] {
	return NewRGATreeSplitNode(initialNodeID, &ZeroTokens{})
}

// ZeroTree is the CRDT element of a Zero document.
type ZeroTree struct {
	rgaTreeSplit *RGATreeSplit[*ZeroTokens]
	createdAt    *time.Ticket
	movedAt      *time.Ticket
	removedAt    *time.Ticket
	// schema is the schema the creator's editor gave the tree (nil: the
	// default). It is document state: the model is built with it.
	schema *ZeroSchema
}

// NewZeroTree creates a new instance of ZeroTree.
func NewZeroTree(elements *RGATreeSplit[*ZeroTokens], createdAt *time.Ticket) *ZeroTree {
	elements.SetGhostPurge(true)
	return &ZeroTree{rgaTreeSplit: elements, createdAt: createdAt}
}

// RGATreeSplit returns the underlying RGATreeSplit.
func (t *ZeroTree) RGATreeSplit() *RGATreeSplit[*ZeroTokens] {
	return t.rgaTreeSplit
}

// Nodes returns the nodes, tombstones included.
func (t *ZeroTree) Nodes() []*RGATreeSplitNode[*ZeroTokens] {
	return t.rgaTreeSplit.nodes()
}

// String returns the live tokens.
func (t *ZeroTree) String() string {
	var b strings.Builder
	for _, node := range t.Nodes() {
		if node.removedAt == nil {
			b.WriteString(node.String())
		}
	}
	return b.String()
}

// Marshal returns the Zero content (the model), as clients show it.
func (t *ZeroTree) Marshal() string {
	return t.ContentJSON()
}

// MarshalTokens returns the live tokens as JSON.
func (t *ZeroTree) MarshalTokens() string {
	var parts []string
	for _, node := range t.Nodes() {
		if node.removedAt == nil && node.value.Len() > 0 {
			parts = append(parts, node.Marshal())
		}
	}
	return fmt.Sprintf(`{"zeroTree":[%s]}`, strings.Join(parts, ","))
}

// MetaSize returns the size of the metadata of this element.
func (t *ZeroTree) MetaSize() int {
	size := 0
	for _, ticket := range []*time.Ticket{t.createdAt, t.movedAt, t.removedAt} {
		if ticket != nil {
			size += time.TicketSize
		}
	}
	return size
}

// DataSize returns the data usage of the live nodes.
func (t *ZeroTree) DataSize() resource.DataSize {
	size := resource.DataSize{}
	for _, node := range t.Nodes() {
		if node.removedAt == nil {
			s := node.DataSize()
			size.Data += s.Data
			size.Meta += s.Meta
		}
	}
	size.Meta += t.MetaSize()
	return size
}

// DeepCopy copies itself deeply, removal ledgers and purged ledgers included.
func (t *ZeroTree) DeepCopy() (Element, error) {
	rga := NewRGATreeSplit(InitialZeroTreeNode())
	current := rga.InitialHead()
	for _, node := range t.Nodes() {
		current = rga.InsertAfter(current, node.DeepCopy())
		if insPrevID := node.InsPrevID(); insPrevID != nil {
			insPrev := rga.FindNode(insPrevID)
			if insPrev == nil {
				return nil, fmt.Errorf("insPrevNode should be presence")
			}
			current.SetInsPrev(insPrev)
		}
	}
	t.rgaTreeSplit.PurgedLedgers(func(createdAt *time.Ticket, entries []*PurgedLedger) {
		for _, e := range entries {
			rga.AddPurgedLedger(createdAt, &PurgedLedger{
				Start: e.Start, End: e.End, Dels: append([]*time.Ticket(nil), e.Dels...),
			})
		}
	})
	tree := NewZeroTree(rga, t.createdAt)
	tree.schema = t.schema
	tree.movedAt = t.movedAt
	tree.removedAt = t.removedAt
	return tree, nil
}

// GCPairs returns the tombstoned nodes.
func (t *ZeroTree) GCPairs() []GCPair {
	var pairs []GCPair
	for _, node := range t.Nodes() {
		if node.removedAt != nil {
			gcSize := node.DataSize()
			pairs = append(pairs, GCPair{Parent: t.rgaTreeSplit, Child: node, GCOnlySize: &gcSize})
		}
	}
	return pairs
}

// CreatedAt returns the creation time.
func (t *ZeroTree) CreatedAt() *time.Ticket { return t.createdAt }

// SetCreatedAt sets the creation time.
func (t *ZeroTree) SetCreatedAt(createdAt *time.Ticket) { t.createdAt = createdAt }

// MovedAt returns the move time.
func (t *ZeroTree) MovedAt() *time.Ticket { return t.movedAt }

// SetMovedAt sets the move time.
func (t *ZeroTree) SetMovedAt(movedAt *time.Ticket) { t.movedAt = movedAt }

// RemovedAt returns the removal time.
func (t *ZeroTree) RemovedAt() *time.Ticket { return t.removedAt }

// SetRemovedAt sets the removal time.
func (t *ZeroTree) SetRemovedAt(removedAt *time.Ticket) { t.removedAt = removedAt }

// Remove removes this element.
func (t *ZeroTree) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(t.createdAt)) &&
		(t.removedAt == nil || removedAt.After(t.removedAt)) {
		t.removedAt = removedAt
		return true
	}
	return false
}

// Edit deletes [from, to) and inserts content (if any) at from. The removed
// spans carry a copy of the removed tokens and the deletion (RemovedBy), so
// an undo cancels exactly this deletion.
func (t *ZeroTree) Edit(
	from, to *RGATreeSplitNodePos,
	content *ZeroTokens,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, []*ZeroRestoreSpan, error) {
	if content == nil {
		content = &ZeroTokens{}
	}
	_, pairs, diff, removed, err := t.rgaTreeSplit.edit(from, to, content, executedAt, versionVector)
	if err != nil {
		return pairs, diff, nil, err
	}
	spans := make([]*ZeroRestoreSpan, 0, len(removed))
	for _, span := range removed {
		spans = append(spans, &ZeroRestoreSpan{
			CreatedAt: span.createdAt,
			Start:     span.start,
			End:       span.end,
			Value:     span.value.DeepCopy().(*ZeroTokens),
			RemovedBy: executedAt,
		})
	}
	return pairs, diff, spans, nil
}

func toRestoreSpanValues(spans []*ZeroRestoreSpan) []restoreSpanValue[*ZeroTokens] {
	internal := make([]restoreSpanValue[*ZeroTokens], 0, len(spans))
	for _, s := range spans {
		internal = append(internal, restoreSpanValue[*ZeroTokens]{
			createdAt: s.CreatedAt,
			start:     s.Start,
			end:       s.End,
			value:     s.Value,
			removedBy: s.RemovedBy,
		})
	}
	return internal
}

// Restore revives the spans under their original identities (undo).
func (t *ZeroTree) Restore(
	spans []*ZeroRestoreSpan,
	executedAt *time.Ticket,
	from *RGATreeSplitNodePos,
) (untombstoned, recreated []*RGATreeSplitNode[*ZeroTokens], stillTombstoned []GCPair, err error) {
	return t.rgaTreeSplit.restore(toRestoreSpanValues(spans), from, executedAt)
}

// Retombstone re-removes the spans under their original identities (redo).
func (t *ZeroTree) Retombstone(
	spans []*ZeroRestoreSpan,
	executedAt *time.Ticket,
	versionVector time.VersionVector,
) ([]GCPair, resource.DataSize, error) {
	return t.rgaTreeSplit.retombstone(toRestoreSpanValues(spans), executedAt, versionVector)
}

// NormalizePos converts the position into a single absolute offset.
func (t *ZeroTree) NormalizePos(pos *RGATreeSplitNodePos) (*RGATreeSplitNodePos, error) {
	return t.rgaTreeSplit.normalizePos(pos)
}

// FindMarker returns the marker of the given absolute id, live or not.
func (t *ZeroTree) FindMarker(id *RGATreeSplitNodeID) *ZeroMarker {
	node := t.rgaTreeSplit.FindNode(id)
	if node == nil {
		return nil
	}
	idx := id.offset - node.id.offset
	if idx < 0 || idx >= len(node.value.Tokens) {
		return nil
	}
	return node.value.Tokens[idx].Marker
}

// SetAttrs writes attributes on the marker of the given id. It returns the
// previous values of the keys that applied, for the reverse operation.
func (t *ZeroTree) SetAttrs(
	markerID *RGATreeSplitNodeID,
	writes []ZeroAttrWrite,
	executedAt *time.Ticket,
) []ZeroAttrWrite {
	marker := t.FindMarker(markerID)
	if marker == nil {
		return nil
	}
	var prev []ZeroAttrWrite
	for _, w := range writes {
		old, applied := marker.Set(w.Key, w.Value, w.Removed, executedAt)
		if !applied {
			continue
		}
		if old == nil {
			prev = append(prev, ZeroAttrWrite{Key: w.Key, Removed: true})
		} else {
			prev = append(prev, ZeroAttrWrite{Key: w.Key, Value: old.Value, Removed: old.Removed})
		}
	}
	return prev
}

// ToTestString shows every node, tombstones included.
func (t *ZeroTree) ToTestString() string {
	return t.rgaTreeSplit.ToTestString()
}
