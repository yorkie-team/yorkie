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
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ZeroAttr is one LWW attribute register of a marker. Removed means the
// value is null; the register is kept so an older write cannot bring it back.
//
// A register a write reached keeps the newest writes (History, at most
// ZeroHistoryDepth, newest first) so an undo can cancel one of them wherever
// it went (see ZeroStyleCancels): the value is then that of the newest write
// no undo cancelled (JS ZeroAttr). Reserved keys (zt.*) stay plain registers.
type ZeroAttr struct {
	Key       string
	Value     string
	Removed   bool
	UpdatedAt *time.Ticket
	History   []*ZeroAttrEntry
}

// ZeroAttrEntry is one write a register keeps (see ZeroAttr.History).
// ByRange: a range write gave it to this marker, which lay in the range,
// rather than someone writing on the marker's node (see dealias).
type ZeroAttrEntry struct {
	Value   string
	Removed bool
	At      *time.Ticket
	ByRange bool
}

// ZeroHistoryDepth is how many writes a register keeps. An undo that cancels
// all of them falls back to what its write's marker held before.
const ZeroHistoryDepth = 4

// keepsHistory tells whether a register of key keeps its writes: the reserved
// keys (zt.*) are bookkeeping, undone by writing them back.
func keepsHistory(key string) bool {
	return !strings.HasPrefix(key, "zt.")
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
// before the write (nil: none) and whether the write applied. With cancels a
// register of a non-reserved key keeps the write in its history and takes the
// newest write not cancelled; byRange marks a write a range write gave it.
func (m *ZeroMarker) Set(
	key, value string,
	removed bool,
	ticket *time.Ticket,
	cancels *ZeroStyleCancels,
	byRange bool,
) (*ZeroAttr, bool) {
	at := -1
	var prev *ZeroAttr
	for i, attr := range m.Attrs {
		if attr.Key == key {
			at, prev = i, attr
			break
		}
	}
	if cancels == nil || !keepsHistory(key) {
		if prev != nil && !ticket.After(prev.UpdatedAt) {
			return prev, false
		}
		m.put(at, &ZeroAttr{Key: key, Value: value, Removed: removed, UpdatedAt: ticket})
		return prev, true
	}
	var history []*ZeroAttrEntry
	if prev != nil {
		if prev.History != nil {
			history = append(history, prev.History...)
		} else {
			history = append(history, &ZeroAttrEntry{Value: prev.Value, Removed: prev.Removed, At: prev.UpdatedAt})
		}
	}
	seen := false
	for _, e := range history {
		if e.At.Compare(ticket) == 0 {
			seen = true
			break
		}
	}
	if !seen {
		history = append(history, &ZeroAttrEntry{Value: value, Removed: removed, At: ticket, ByRange: byRange})
		sortHistory(history)
		if len(history) > ZeroHistoryDepth {
			history = history[:ZeroHistoryDepth]
		}
	}
	picked := cancels.pick(key, history)
	m.put(at, &ZeroAttr{Key: key, Value: picked.Value, Removed: picked.Removed, UpdatedAt: picked.At, History: history})
	return prev, picked.At.Compare(ticket) == 0
}

// put replaces the register at index at, or adds it when at is -1.
func (m *ZeroMarker) put(at int, attr *ZeroAttr) {
	if at < 0 {
		m.Attrs = append(m.Attrs, attr)
		return
	}
	m.Attrs[at] = attr
}

// sortHistory orders kept writes newest first.
func sortHistory(history []*ZeroAttrEntry) {
	for i := 1; i < len(history); i++ {
		for j := i; j > 0 && history[j].At.After(history[j-1].At); j-- {
			history[j], history[j-1] = history[j-1], history[j]
		}
	}
}

// holds tells whether a register of this marker keeps the write of ticket.
func (m *ZeroMarker) holds(ticket *time.Ticket) bool {
	for _, attr := range m.Attrs {
		for _, e := range attr.History {
			if e.At.Compare(ticket) == 0 {
				return true
			}
		}
	}
	return false
}

// repick makes every register with a history take its value again under
// cancels.
func (m *ZeroMarker) repick(cancels *ZeroStyleCancels) {
	for i, attr := range m.Attrs {
		if attr.History == nil {
			continue
		}
		picked := cancels.pick(attr.Key, attr.History)
		m.Attrs[i] = &ZeroAttr{
			Key: attr.Key, Value: picked.Value, Removed: picked.Removed,
			UpdatedAt: picked.At, History: attr.History,
		}
	}
}

// DeepCopy copies this marker.
func (m *ZeroMarker) DeepCopy() *ZeroMarker {
	attrs := make([]*ZeroAttr, len(m.Attrs))
	for i, attr := range m.Attrs {
		cp := *attr
		if attr.History != nil {
			cp.History = make([]*ZeroAttrEntry, len(attr.History))
			for j, e := range attr.History {
				ce := *e
				cp.History[j] = &ce
			}
		}
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
			for _, e := range attr.History {
				size.Data += utf16Len(e.Value) * 2
				size.Meta += time.TicketSize
			}
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

// ZeroRegionPrefix names the register a marker keeps a pending range write
// in, keyed by the write's ticket, so a snapshot carries it (see
// ZeroStyleRegion).
const ZeroRegionPrefix = "zt.r."

// ZeroStyleRange makes a style write on a text node a range: up to the last
// character of the node in its author's view, or to the end of the document
// when Last is nil.
type ZeroStyleRange struct {
	Last *RGATreeSplitNodeID
}

// ZeroStyleRegion is a style write on a text node taken as a range: from the
// node's marker to the last character of the node in its author's view
// (Last). Every marker of the same type between the two in the token order
// gets the write too (LWW, at the write's ticket), so a node someone split off
// concurrently keeps the style written on what it was split from.
//
// The range ends at that character, not at the token after it: a marker
// someone put concurrently right after the node's text opens what follows the
// range, so it does not get the write. The token order between two tokens
// never changes, so whether a marker falls in the range does not depend on
// when anything arrives.
//
// The markers between the two when the write arrives get it then. A marker
// that arrives later gets it if it is concurrent with the write (its change
// does not know the write). So the write stays pending until every replica
// knows it -- it is a GC pair whose removal time is its ticket -- and the
// marker keeps it in a register (ZeroRegionPrefix) until then, for
// snapshots. Mirrors the JS SDK's ZeroStyleRegion.
type ZeroStyleRegion struct {
	MarkerID *RGATreeSplitNodeID
	Type     string
	Last     *RGATreeSplitNodeID
	Writes   []ZeroAttrWrite
	Ticket   *time.Ticket
}

// key returns the register the start marker keeps this write in.
func (r *ZeroStyleRegion) key() string {
	return ZeroRegionPrefix + zeroTicketIDString(r.Ticket)
}

// IDString returns a string that identifies this write.
func (r *ZeroStyleRegion) IDString() string {
	return zeroIDString(r.MarkerID.createdAt, r.MarkerID.offset) + "#" + zeroTicketIDString(r.Ticket)
}

// RemovedAt returns when this write may be dropped: once it is known
// everywhere, no marker concurrent with it can arrive.
func (r *ZeroStyleRegion) RemovedAt() *time.Ticket { return r.Ticket }

// DataSize returns nothing: the register is part of the marker.
func (r *ZeroStyleRegion) DataSize() resource.DataSize { return resource.DataSize{} }

// encode returns what the start marker keeps of this write, byte for byte
// what the JS SDK writes.
func (r *ZeroStyleRegion) encode() string {
	last := ""
	if r.Last != nil {
		last = encodeZeroNodeID(r.Last)
	}
	writes := make([]any, 0, len(r.Writes))
	for _, w := range r.Writes {
		if w.Removed {
			writes = append(writes, []any{w.Key, nil})
		} else {
			writes = append(writes, []any{w.Key, w.Value})
		}
	}
	return `{"l":` + jsonString(last) + `,"w":` + jsonString(writes) + `}`
}

// jsonString encodes v as JSON.stringify does for the values used here.
func jsonString(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// zeroTicketIDString is the JS SDK's TimeTicket.toIDString.
func zeroTicketIDString(t *time.Ticket) string {
	return strconv.FormatInt(t.Lamport(), 10) + ":" + t.ActorID().String() + ":" +
		strconv.FormatUint(uint64(t.Delimiter()), 10)
}

// encodeZeroNodeID is the JS SDK's encodeNodeID.
func encodeZeroNodeID(id *RGATreeSplitNodeID) string {
	at := id.createdAt
	return jsonString([]any{strconv.FormatInt(at.Lamport(), 10), at.Delimiter(), at.ActorID().String(), id.offset})
}

// decodeZeroNodeID decodes what encodeZeroNodeID made.
func decodeZeroNodeID(value string) (*RGATreeSplitNodeID, error) {
	var lamportStr, actor string
	var delimiter uint32
	var offset int
	parts := []any{&lamportStr, &delimiter, &actor, &offset}
	if err := json.Unmarshal([]byte(value), &parts); err != nil {
		return nil, fmt.Errorf("decode zero node id %q: %w", value, err)
	}
	lamport, err := strconv.ParseInt(lamportStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("decode zero node id %q: %w", value, err)
	}
	actorID, err := time.ActorIDFromHex(actor)
	if err != nil {
		return nil, fmt.Errorf("decode zero node id %q: %w", value, err)
	}
	return NewRGATreeSplitNodeID(time.NewTicket(lamport, delimiter, actorID), offset), nil
}

// ZeroCancelWrite is what an undo (or redo) of a style write carries: cancel
// (On) or bring back the write of Write; Fallback is what the write's marker
// held before it (see ZeroStyleCancels.pick).
type ZeroCancelWrite struct {
	Write    *time.Ticket
	On       bool
	Fallback []ZeroAttrWrite
}

// ZeroStyleCancel is the state of one style write's undo in a tree: set by
// the undo or redo of At (last writer wins). It may be dropped once every
// replica knows it and no register keeps the write (see PurgeBarrierAt).
type ZeroStyleCancel struct {
	Write    *time.Ticket
	On       bool
	At       *time.Ticket
	Fallback []ZeroAttrWrite
}

// IDString returns a string that identifies this state.
func (c *ZeroStyleCancel) IDString() string {
	return c.Write.Key() + "@" + c.At.Key()
}

// RemovedAt returns when this state may be dropped (see PurgeBarrierAt for
// the other condition).
func (c *ZeroStyleCancel) RemovedAt() *time.Ticket { return c.At }

// DataSize returns nothing: it is bookkeeping of the tree.
func (c *ZeroStyleCancel) DataSize() resource.DataSize { return resource.DataSize{} }

// ZeroStyleCancels is a tree's table of undone style writes (JS
// ZeroStyleCancels): a register skips a cancelled write when it picks its
// value, so an undo takes back exactly its write wherever it went.
type ZeroStyleCancels struct {
	m map[string]*ZeroStyleCancel
}

func (cs *ZeroStyleCancels) get(write *time.Ticket) *ZeroStyleCancel {
	if cs == nil || cs.m == nil {
		return nil
	}
	return cs.m[write.Key()]
}

// set keeps c unless a later undo or redo of its write is known.
func (cs *ZeroStyleCancels) set(c *ZeroStyleCancel) bool {
	if cs.m == nil {
		cs.m = make(map[string]*ZeroStyleCancel)
	}
	if cur := cs.m[c.Write.Key()]; cur != nil && !c.At.After(cur.At) {
		return false
	}
	cs.m[c.Write.Key()] = c
	return true
}

func (cs *ZeroStyleCancels) isCurrent(c *ZeroStyleCancel) bool {
	return cs.get(c.Write) == c
}

func (cs *ZeroStyleCancels) isCancelled(ticket *time.Ticket) bool {
	c := cs.get(ticket)
	return c != nil && c.On
}

// values returns the states in the order of their writes' tickets.
func (cs *ZeroStyleCancels) values() []*ZeroStyleCancel {
	out := make([]*ZeroStyleCancel, 0, len(cs.m))
	for _, c := range cs.m {
		out = append(out, c)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Write.Compare(out[j-1].Write) < 0; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// pick returns the write a register keeping history (newest first, never
// empty) shows: the newest one not cancelled, or, when all are, what the
// oldest one's marker held before it.
func (cs *ZeroStyleCancels) pick(key string, history []*ZeroAttrEntry) *ZeroAttrEntry {
	for _, e := range history {
		if !cs.isCancelled(e.At) {
			return e
		}
	}
	// the value before predates every write the register keeps: it carries
	// the initial ticket, so a value it meets in a merge or a copy wins
	oldest := history[len(history)-1]
	if c := cs.get(oldest.At); c != nil {
		for _, w := range c.Fallback {
			if w.Key == key {
				return &ZeroAttrEntry{Value: w.Value, Removed: w.Removed, At: time.InitialTicket}
			}
		}
	}
	return &ZeroAttrEntry{Removed: true, At: time.InitialTicket}
}

func (cs *ZeroStyleCancels) deepCopy() *ZeroStyleCancels {
	clone := &ZeroStyleCancels{}
	for k, v := range cs.m {
		if clone.m == nil {
			clone.m = make(map[string]*ZeroStyleCancel)
		}
		clone.m[k] = v
	}
	return clone
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
	// pending is the range style writes still pending, by key, read from the
	// markers that keep them the first time it is needed (see
	// ZeroStyleRegion).
	pending map[string]*ZeroStyleRegion
	// cancels is the style writes undone or redone (see ZeroStyleCancels).
	cancels ZeroStyleCancels
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
	tree.cancels = *t.cancels.deepCopy()
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
	for _, region := range t.pendingRegions() {
		size := region.DataSize()
		pairs = append(pairs, GCPair{Parent: t, Child: region, GCOnlySize: &size})
	}
	for _, cancel := range t.cancels.values() {
		size := cancel.DataSize()
		pairs = append(pairs, GCPair{Parent: t, Child: cancel, GCOnlySize: &size})
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
	if len(content.Tokens) > 0 {
		t.applyPending(executedAt, versionVector)
		t.settleInserted(t.rgaTreeSplit.FindNode(NewRGATreeSplitNodeID(executedAt, 0)))
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
	untombstoned, recreated, stillTombstoned, err = t.rgaTreeSplit.restore(toRestoreSpanValues(spans), from, executedAt)
	// a piece made again from its span: the span's copy of the markers
	// predates the undos since
	for _, node := range recreated {
		t.settleInserted(node)
	}
	return untombstoned, recreated, stillTombstoned, err
}

// settleInserted makes the markers of an inserted run take their values under
// the cancels: a copy made without knowing an undo keeps the write it
// cancelled.
func (t *ZeroTree) settleInserted(node *RGATreeSplitNode[*ZeroTokens]) {
	if node == nil || node.value == nil || len(t.cancels.m) == 0 {
		return
	}
	for _, tok := range node.value.Tokens {
		if tok.Marker != nil {
			tok.Marker.repick(&t.cancels)
		}
	}
}

// IsCancelled tells whether an undo cancelled the style write of ticket.
func (t *ZeroTree) IsCancelled(ticket *time.Ticket) bool {
	return t.cancels.isCancelled(ticket)
}

// StyleCancels returns the undone style writes in the order of their tickets.
func (t *ZeroTree) StyleCancels() []*ZeroStyleCancel {
	return t.cancels.values()
}

// LoadStyleCancel keeps a state read from a snapshot.
func (t *ZeroTree) LoadStyleCancel(c *ZeroStyleCancel) {
	t.cancels.set(c)
}

// holders returns the markers whose registers keep the write of ticket, live
// or not.
func (t *ZeroTree) holders(ticket *time.Ticket) []*ZeroMarker {
	var out []*ZeroMarker
	for _, node := range t.Nodes() {
		if node.value == nil {
			continue
		}
		for _, tok := range node.value.Tokens {
			if tok.Marker != nil && tok.Marker.holds(ticket) {
				out = append(out, tok.Marker)
			}
		}
	}
	return out
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

// SetAttrs writes attributes on the marker of the given id. It returns, for
// the reverse operation, the previous values of the reserved keys that
// applied (written back on undo) and the cancel of this write (or, for an
// undo or redo, of the write it undid), and the GC pairs of a range write and
// of a cancel. With rng the write is a range (see ZeroStyleRegion); cancel
// cancels or brings back an earlier write (see ZeroStyleCancels).
func (t *ZeroTree) SetAttrs(
	markerID *RGATreeSplitNodeID,
	writes []ZeroAttrWrite,
	executedAt *time.Ticket,
	rng *ZeroStyleRange,
	cancel *ZeroCancelWrite,
) ([]ZeroAttrWrite, []GCPair, *ZeroCancelWrite) {
	marker := t.FindMarker(markerID)
	var prev []ZeroAttrWrite
	var pairs []GCPair
	var reverse *ZeroCancelWrite
	if marker != nil && len(writes) > 0 {
		var fallback []ZeroAttrWrite
		for _, w := range writes {
			old, applied := marker.Set(w.Key, w.Value, w.Removed, executedAt, &t.cancels, false)
			if !applied {
				continue
			}
			before := ZeroAttrWrite{Key: w.Key, Removed: true}
			if old != nil {
				before = ZeroAttrWrite{Key: w.Key, Value: old.Value, Removed: old.Removed}
			}
			if keepsHistory(w.Key) {
				fallback = append(fallback, before)
			} else {
				prev = append(prev, before)
			}
		}
		if len(fallback) > 0 {
			reverse = &ZeroCancelWrite{Write: executedAt, On: true, Fallback: fallback}
		}
		if rng != nil {
			region := &ZeroStyleRegion{
				MarkerID: markerID,
				Type:     marker.Type,
				Last:     rng.Last,
				Writes:   writes,
				Ticket:   executedAt,
			}
			marker.setRaw(region.key(), region.encode(), executedAt)
			t.pendingRegions()[region.key()] = region
			t.applyRegion(region, nil)
			size := region.DataSize()
			pairs = append(pairs, GCPair{Parent: t, Child: region, GCOnlySize: &size})
		}
	}
	if cancel != nil {
		state := &ZeroStyleCancel{Write: cancel.Write, On: cancel.On, At: executedAt, Fallback: cancel.Fallback}
		if t.cancels.set(state) {
			size := state.DataSize()
			pairs = append(pairs, GCPair{Parent: t, Child: state, GCOnlySize: &size})
			for _, m := range t.holders(cancel.Write) {
				m.repick(&t.cancels)
			}
		}
		reverse = &ZeroCancelWrite{Write: cancel.Write, On: !cancel.On, Fallback: cancel.Fallback}
	}
	return prev, pairs, reverse
}

// setRaw writes a register as it is, without last-writer-wins: a pending
// range write's register (one key per write).
func (m *ZeroMarker) setRaw(key, value string, ticket *time.Ticket) {
	for i, attr := range m.Attrs {
		if attr.Key == key {
			m.Attrs[i] = &ZeroAttr{Key: key, Value: value, UpdatedAt: ticket}
			return
		}
	}
	m.Attrs = append(m.Attrs, &ZeroAttr{Key: key, Value: value, UpdatedAt: ticket})
}

// pendingRegions returns the range writes still pending, read from the
// markers that keep them the first time (a snapshot, a copy).
func (t *ZeroTree) pendingRegions() map[string]*ZeroStyleRegion {
	if t.pending != nil {
		return t.pending
	}
	t.pending = make(map[string]*ZeroStyleRegion)
	for _, node := range t.Nodes() {
		if node.value == nil {
			continue
		}
		for i, tok := range node.value.Tokens {
			if tok.Marker == nil {
				continue
			}
			for _, attr := range tok.Marker.Attrs {
				if !strings.HasPrefix(attr.Key, ZeroRegionPrefix) || attr.Removed {
					continue
				}
				region, err := decodeZeroRegion(attr.Value)
				if err != nil {
					continue
				}
				region.MarkerID = NewRGATreeSplitNodeID(node.id.createdAt, node.id.offset+i)
				region.Type = tok.Marker.Type
				region.Ticket = attr.UpdatedAt
				t.pending[attr.Key] = region
			}
		}
	}
	return t.pending
}

// decodeZeroRegion reads what ZeroStyleRegion.encode wrote.
func decodeZeroRegion(value string) (*ZeroStyleRegion, error) {
	var raw struct {
		L string      `json:"l"`
		W [][]*string `json:"w"`
	}
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, fmt.Errorf("decode zero style region %q: %w", value, err)
	}
	region := &ZeroStyleRegion{}
	if raw.L != "" {
		last, err := decodeZeroNodeID(raw.L)
		if err != nil {
			return nil, err
		}
		region.Last = last
	}
	for _, w := range raw.W {
		if len(w) != 2 || w[0] == nil {
			return nil, fmt.Errorf("decode zero style region %q: bad write", value)
		}
		if w[1] == nil {
			region.Writes = append(region.Writes, ZeroAttrWrite{Key: *w[0], Removed: true})
		} else {
			region.Writes = append(region.Writes, ZeroAttrWrite{Key: *w[0], Value: *w[1]})
		}
	}
	return region, nil
}

// applyRegion writes a range write on the markers of its type between its
// marker and Last, removed ones included (an undo may bring them back), or
// only on target when it is given. It returns whether it reached target.
func (t *ZeroTree) applyRegion(region *ZeroStyleRegion, target *ZeroMarker) bool {
	node := t.rgaTreeSplit.FindNode(region.MarkerID)
	if node == nil {
		return false
	}
	until := region.Last
	i := region.MarkerID.offset - node.id.offset + 1
	for ; node != nil; node = node.next {
		if node.value != nil {
			last := until != nil && node.id.createdAt.Compare(until.createdAt) == 0
			for ; i < len(node.value.Tokens); i++ {
				if last && node.id.offset+i == until.offset {
					return false
				}
				m := node.value.Tokens[i].Marker
				if m == nil || m.Type != region.Type {
					continue
				}
				if target == nil || m == target {
					for _, w := range region.Writes {
						m.Set(w.Key, w.Value, w.Removed, region.Ticket, &t.cancels, true)
					}
					if target != nil {
						return true
					}
				}
			}
		}
		i = 0
	}
	return false
}

// applyPending gives the markers of a run inserted by a change that did not
// know a pending range write the write, when they fall in its range.
func (t *ZeroTree) applyPending(executedAt *time.Ticket, versionVector time.VersionVector) {
	if len(versionVector) == 0 {
		return
	}
	pending := t.pendingRegions()
	if len(pending) == 0 {
		return
	}
	var regions []*ZeroStyleRegion
	for _, region := range pending {
		if !versionVector.EqualToOrAfter(region.Ticket) {
			regions = append(regions, region)
		}
	}
	if len(regions) == 0 {
		return
	}
	// in ticket order, as in JS: the writes do not depend on it (LWW), but
	// the order a marker first gets its keys in is part of its encoding
	sortRegions(regions)
	node := t.rgaTreeSplit.FindNode(NewRGATreeSplitNodeID(executedAt, 0))
	if node == nil || node.value == nil {
		return
	}
	for _, tok := range node.value.Tokens {
		if tok.Marker == nil {
			continue
		}
		for _, region := range regions {
			if region.Type == tok.Marker.Type {
				t.applyRegion(region, tok.Marker)
			}
		}
	}
}

func sortRegions(regions []*ZeroStyleRegion) {
	for i := 1; i < len(regions); i++ {
		for j := i; j > 0 && regions[j].Ticket.Compare(regions[j-1].Ticket) < 0; j-- {
			regions[j], regions[j-1] = regions[j-1], regions[j]
		}
	}
}

// PurgeBarrierAt holds back the state of an undone write while a register
// keeps the write or its range write is pending: either can still pick it.
func (t *ZeroTree) PurgeBarrierAt(child GCChild) *time.Ticket {
	cancel, ok := child.(*ZeroStyleCancel)
	if !ok || !t.cancels.isCurrent(cancel) {
		return nil
	}
	if _, pending := t.pendingRegions()[ZeroRegionPrefix+zeroTicketIDString(cancel.Write)]; pending ||
		len(t.holders(cancel.Write)) > 0 {
		return time.MaxTicket
	}
	return nil
}

// Purge drops a range write once every replica knows it, and the state of an
// undone write once every replica knows it and nothing keeps the write (see
// PurgeBarrierAt).
func (t *ZeroTree) Purge(child GCChild) error {
	if cancel, ok := child.(*ZeroStyleCancel); ok {
		if t.cancels.isCurrent(cancel) {
			delete(t.cancels.m, cancel.Write.Key())
		}
		return nil
	}
	region, ok := child.(*ZeroStyleRegion)
	if !ok {
		return nil
	}
	delete(t.pendingRegions(), region.key())
	if marker := t.FindMarker(region.MarkerID); marker != nil {
		for i, attr := range marker.Attrs {
			if attr.Key == region.key() {
				marker.Attrs = append(marker.Attrs[:i], marker.Attrs[i+1:]...)
				break
			}
		}
	}
	return nil
}

// ToTestString shows every node, tombstones included.
func (t *ZeroTree) ToTestString() string {
	return t.rgaTreeSplit.ToTestString()
}
