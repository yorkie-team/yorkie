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

package converter

// PROTOTYPE -- protobuf conversion of crdt.ZeroTree and its operations. The
// encoding follows the JS SDK's converter.ts field by field (consecutive
// characters as one string, insPrev only to an emitted node, purged ledgers
// in insertion order), so the two produce the same bytes for the same state.

import (
	goerrors "errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// zeroActors is the actor table of a ZeroTree element (see the JS SDK's
// ZeroActors): tickets name an actor by index into the sorted actor ids.
type zeroActors struct {
	ids   []time.ActorID
	index map[string]uint32
}

func zeroActorsOf(tree *crdt.ZeroTree) *zeroActors {
	seen := map[string]time.ActorID{}
	add := func(t *time.Ticket) {
		if t != nil {
			seen[t.ActorID().String()] = t.ActorID()
		}
	}
	for _, node := range tree.Nodes() {
		add(node.ID().CreatedAt())
		add(node.RemovedAt())
		_, dels, cancels := node.Ledger()
		for _, d := range dels {
			add(d)
		}
		for _, c := range cancels {
			add(c.Deletion)
			add(c.CancelledAt)
		}
		for _, tok := range node.Value().Tokens {
			if tok.Marker != nil {
				for _, attr := range tok.Marker.Attrs {
					add(attr.UpdatedAt)
				}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	a := &zeroActors{index: map[string]uint32{}}
	for i, k := range keys {
		a.ids = append(a.ids, seen[k])
		a.index[k] = uint32(i)
	}
	return a
}

func (a *zeroActors) ticket(t *time.Ticket) *api.ZeroTicket {
	if t == nil {
		return nil
	}
	return &api.ZeroTicket{Lamport: t.Lamport(), Delimiter: t.Delimiter(), Actor: a.index[t.ActorID().String()]}
}

func (a *zeroActors) nodeID(id *crdt.RGATreeSplitNodeID) *api.ZeroNodeID {
	return &api.ZeroNodeID{CreatedAt: a.ticket(id.CreatedAt()), Offset: int32(id.Offset())}
}

func (a *zeroActors) fromTicket(t *api.ZeroTicket) (*time.Ticket, error) {
	if t == nil {
		return nil, nil
	}
	if int(t.Actor) >= len(a.ids) {
		return nil, goerrors.New("unknown zero tree actor")
	}
	return time.NewTicket(t.Lamport, t.Delimiter, a.ids[t.Actor]), nil
}

func (a *zeroActors) fromRequired(t *api.ZeroTicket, what string) (*time.Ticket, error) {
	ticket, err := a.fromTicket(t)
	if err == nil && ticket == nil {
		err = fmt.Errorf("%s: %w", what, ErrMissingTicket)
	}
	return ticket, err
}

func (a *zeroActors) fromNodeID(id *api.ZeroNodeID) (*crdt.RGATreeSplitNodeID, error) {
	if id == nil {
		return nil, nil
	}
	createdAt, err := a.fromRequired(id.CreatedAt, "zero_node_id.created_at")
	if err != nil {
		return nil, err
	}
	return crdt.NewRGATreeSplitNodeID(createdAt, int(id.Offset)), nil
}

func toZeroTokens(value *crdt.ZeroTokens, actors ...*zeroActors) *api.ZeroTokens {
	if value == nil {
		return nil
	}
	pbTokens := &api.ZeroTokens{Scope: value.Scope}
	var chars []rune
	ghosts := uint32(0)
	flush := func() {
		if len(chars) > 0 {
			pbTokens.Tokens = append(pbTokens.Tokens, &api.ZeroToken{
				Body: &api.ZeroToken_Chars{Chars: string(chars)},
			})
			chars = nil
		}
		if ghosts > 0 {
			pbTokens.Tokens = append(pbTokens.Tokens, &api.ZeroToken{
				Body: &api.ZeroToken_Ghost{Ghost: ghosts},
			})
			ghosts = 0
		}
	}
	for _, t := range value.Tokens {
		// A NUL character is the JS SDK's ghost placeholder: same encoding.
		if t.Ghost || (t.Marker == nil && t.Char == 0) {
			if len(chars) > 0 {
				flush()
			}
			ghosts++
			continue
		}
		if ghosts > 0 {
			flush()
		}
		if t.Marker == nil {
			chars = append(chars, t.Char)
			continue
		}
		flush()
		pbMarker := &api.ZeroMarker{
			Type:     t.Marker.Type,
			Depth:    int32(t.Marker.Depth),
			Boundary: t.Marker.Boundary,
		}
		for _, attr := range t.Marker.Attrs {
			pbAttr := &api.ZeroAttr{
				Key:       attr.Key,
				Value:     attr.Value,
				UpdatedAt: ToTimeTicket(attr.UpdatedAt),
				IsRemoved: attr.Removed,
			}
			if len(actors) > 0 {
				pbAttr.UpdatedAt, pbAttr.UpdatedAtRef = nil, actors[0].ticket(attr.UpdatedAt)
			}
			pbMarker.Attrs = append(pbMarker.Attrs, pbAttr)
		}
		pbTokens.Tokens = append(pbTokens.Tokens, &api.ZeroToken{
			Body: &api.ZeroToken_Marker{Marker: pbMarker},
		})
	}
	flush()
	return pbTokens
}

func fromZeroTokens(pbTokens *api.ZeroTokens, actors ...*zeroActors) (*crdt.ZeroTokens, error) {
	if pbTokens == nil {
		return nil, goerrors.New("zero tokens missing")
	}
	var tokens []crdt.ZeroToken
	for _, pbToken := range pbTokens.Tokens {
		switch body := pbToken.Body.(type) {
		case *api.ZeroToken_Chars:
			for _, r := range body.Chars {
				tokens = append(tokens, crdt.ZeroToken{Char: r})
			}
		case *api.ZeroToken_Ghost:
			for i := uint32(0); i < body.Ghost; i++ {
				tokens = append(tokens, crdt.ZeroToken{Ghost: true})
			}
		case *api.ZeroToken_Marker:
			marker := &crdt.ZeroMarker{
				Type:     body.Marker.Type,
				Depth:    int(body.Marker.Depth),
				Boundary: body.Marker.Boundary,
			}
			for _, pbAttr := range body.Marker.Attrs {
				var updatedAt *time.Ticket
				var err error
				if pbAttr.UpdatedAtRef != nil && len(actors) > 0 {
					updatedAt, err = actors[0].fromRequired(pbAttr.UpdatedAtRef, "zero_attr.updated_at_ref")
				} else {
					updatedAt, err = fromRequiredTimeTicket(pbAttr.UpdatedAt, "zero_attr.updated_at")
				}
				if err != nil {
					return nil, err
				}
				marker.Attrs = append(marker.Attrs, &crdt.ZeroAttr{
					Key:       pbAttr.Key,
					Value:     pbAttr.Value,
					Removed:   pbAttr.IsRemoved,
					UpdatedAt: updatedAt,
				})
			}
			tokens = append(tokens, crdt.ZeroToken{Marker: marker})
		default:
			return nil, goerrors.New("zero token body missing")
		}
	}
	return crdt.NewZeroTokens(tokens, pbTokens.Scope), nil
}

func toZeroRestoreSpans(spans []*crdt.ZeroRestoreSpan) []*api.ZeroRestoreSpan {
	pbSpans := make([]*api.ZeroRestoreSpan, 0, len(spans))
	for _, span := range spans {
		pbSpans = append(pbSpans, &api.ZeroRestoreSpan{
			CreatedAt: ToTimeTicket(span.CreatedAt),
			Start:     int32(span.Start),
			End:       int32(span.End),
			Value:     toZeroTokens(span.Value),
			RemovedBy: ToTimeTicket(span.RemovedBy),
		})
	}
	return pbSpans
}

func fromZeroRestoreSpans(pbSpans []*api.ZeroRestoreSpan) ([]*crdt.ZeroRestoreSpan, error) {
	spans := make([]*crdt.ZeroRestoreSpan, 0, len(pbSpans))
	for _, pbSpan := range pbSpans {
		createdAt, err := fromRequiredTimeTicket(pbSpan.CreatedAt, "zero_restore_span.created_at")
		if err != nil {
			return nil, err
		}
		if pbSpan.Start < 0 || pbSpan.End < pbSpan.Start {
			return nil, ErrInvalidRestoreSpan
		}
		value, err := fromZeroTokens(pbSpan.Value)
		if err != nil {
			return nil, err
		}
		if value.Len() != int(pbSpan.End-pbSpan.Start) {
			return nil, ErrInvalidRestoreSpan
		}
		removedBy, err := fromTimeTicket(pbSpan.RemovedBy)
		if err != nil {
			return nil, err
		}
		spans = append(spans, &crdt.ZeroRestoreSpan{
			CreatedAt: createdAt,
			Start:     int(pbSpan.Start),
			End:       int(pbSpan.End),
			Value:     value,
			RemovedBy: removedBy,
		})
	}
	return spans, nil
}

func toZeroTreeEdit(e *operations.ZeroTreeEdit) *api.Operation_ZeroTreeEdit_ {
	pbEdit := &api.Operation_ZeroTreeEdit{
		ParentCreatedAt: ToTimeTicket(e.ParentCreatedAt()),
		From:            toTextNodePos(e.From()),
		To:              toTextNodePos(e.To()),
		Content:         toZeroTokens(e.Content()),
		ExecutedAt:      ToTimeTicket(e.ExecutedAt()),
	}
	if e.RestoreMode() != crdt.RestoreModeNone {
		pbEdit.RestoreSpans = toZeroRestoreSpans(e.RestoreSpans())
		pbEdit.RetombstoneSpans = toZeroRestoreSpans(e.RetombstoneSpans())
		pbEdit.RestoreMode = toRestoreMode(e.RestoreMode())
	}
	return &api.Operation_ZeroTreeEdit_{ZeroTreeEdit: pbEdit}
}

func fromZeroTreeEdit(pbEdit *api.Operation_ZeroTreeEdit) (*operations.ZeroTreeEdit, error) {
	if pbEdit == nil {
		return nil, goerrors.New("operation zero tree edit missing")
	}
	parentCreatedAt, err := fromRequiredTimeTicket(pbEdit.ParentCreatedAt, "zero_tree_edit.parent_created_at")
	if err != nil {
		return nil, err
	}
	from, err := fromTextNodePos(pbEdit.From)
	if err != nil {
		return nil, err
	}
	to, err := fromTextNodePos(pbEdit.To)
	if err != nil {
		return nil, err
	}
	executedAt, err := fromRequiredTimeTicket(pbEdit.ExecutedAt, "zero_tree_edit.executed_at")
	if err != nil {
		return nil, err
	}
	var content *crdt.ZeroTokens
	if pbEdit.Content != nil {
		if content, err = fromZeroTokens(pbEdit.Content); err != nil {
			return nil, err
		}
	}
	restoreSpans, err := fromZeroRestoreSpans(pbEdit.RestoreSpans)
	if err != nil {
		return nil, err
	}
	retombstoneSpans, err := fromZeroRestoreSpans(pbEdit.RetombstoneSpans)
	if err != nil {
		return nil, err
	}
	mode := fromRestoreMode(pbEdit.RestoreMode)
	if mode == crdt.RestoreModeNone && (len(restoreSpans) > 0 || len(retombstoneSpans) > 0) {
		return nil, ErrInvalidRestoreSpan
	}
	return operations.NewZeroTreeEdit(parentCreatedAt, from, to, content, executedAt,
		restoreSpans, mode, retombstoneSpans), nil
}

func toZeroTreeStyle(e *operations.ZeroTreeStyle) *api.Operation_ZeroTreeStyle_ {
	pbStyle := &api.Operation_ZeroTreeStyle{
		ParentCreatedAt: ToTimeTicket(e.ParentCreatedAt()),
		MarkerId:        toTextNodeID(e.MarkerID()),
		ExecutedAt:      ToTimeTicket(e.ExecutedAt()),
	}
	for _, w := range e.Writes() {
		pbStyle.Writes = append(pbStyle.Writes, &api.ZeroAttrWrite{
			Key: w.Key, Value: w.Value, IsRemoved: w.Removed,
		})
	}
	if rng := e.Range(); rng != nil {
		pbStyle.Range = &api.ZeroStyleRange{}
		if rng.Until != nil {
			pbStyle.Range.Until = toTextNodeID(rng.Until)
		}
	}
	return &api.Operation_ZeroTreeStyle_{ZeroTreeStyle: pbStyle}
}

func fromZeroTreeStyle(pbStyle *api.Operation_ZeroTreeStyle) (*operations.ZeroTreeStyle, error) {
	if pbStyle == nil {
		return nil, goerrors.New("operation zero tree style missing")
	}
	parentCreatedAt, err := fromRequiredTimeTicket(pbStyle.ParentCreatedAt, "zero_tree_style.parent_created_at")
	if err != nil {
		return nil, err
	}
	markerID, err := fromTextNodeID(pbStyle.MarkerId)
	if err != nil {
		return nil, err
	}
	if markerID == nil {
		return nil, goerrors.New("zero tree style marker missing")
	}
	executedAt, err := fromRequiredTimeTicket(pbStyle.ExecutedAt, "zero_tree_style.executed_at")
	if err != nil {
		return nil, err
	}
	writes := make([]crdt.ZeroAttrWrite, 0, len(pbStyle.Writes))
	for _, w := range pbStyle.Writes {
		writes = append(writes, crdt.ZeroAttrWrite{Key: w.Key, Value: w.Value, Removed: w.IsRemoved})
	}
	var rng *crdt.ZeroStyleRange
	if pbStyle.Range != nil {
		rng = &crdt.ZeroStyleRange{}
		if pbStyle.Range.Until != nil {
			until, err := fromTextNodeID(pbStyle.Range.Until)
			if err != nil {
				return nil, err
			}
			rng.Until = until
		}
	}
	return operations.NewZeroTreeStyle(parentCreatedAt, markerID, writes, executedAt, rng), nil
}

func toZeroTree(tree *crdt.ZeroTree) *api.JSONElement {
	pbTree := &api.JSONElement_ZeroTree{
		CreatedAt: ToTimeTicket(tree.CreatedAt()),
		MovedAt:   ToTimeTicket(tree.MovedAt()),
		RemovedAt: ToTimeTicket(tree.RemovedAt()),
		Schema:    toZeroSchema(tree.EmbeddedSchema()),
	}
	actors := zeroActorsOf(tree)
	for _, id := range actors.ids {
		pbTree.Actors = append(pbTree.Actors, id.Bytes())
	}
	emitted := make(map[string]bool)
	for _, node := range tree.Nodes() {
		ledgered, dels, cancels := node.Ledger()
		// A ledger that says only "removed by removedAt" is what a node
		// without one starts from, so it is not written (same as JS).
		if ledgered && len(cancels) == 0 {
			if removedAt := node.RemovedAt(); (removedAt == nil && len(dels) == 0) ||
				(removedAt != nil && len(dels) == 1 && dels[0].Compare(removedAt) == 0) {
				ledgered, dels, cancels = false, nil, nil
			}
		}
		pbNode := &api.ZeroTreeNode{
			Id:        actors.nodeID(node.ID()),
			Value:     toZeroTokens(node.Value(), actors),
			RemovedAt: actors.ticket(node.RemovedAt()),
			HasLedger: ledgered,
		}
		for _, d := range dels {
			pbNode.Dels = append(pbNode.Dels, actors.ticket(d))
		}
		for _, c := range cancels {
			pbNode.Cancels = append(pbNode.Cancels, &api.ZeroCancel{
				Deletion:    actors.ticket(c.Deletion),
				CancelledAt: actors.ticket(c.CancelledAt),
			})
		}
		// Same rule as the JS encoder: only a link to an emitted node.
		if insPrevID := node.InsPrevID(); insPrevID != nil && emitted[nodeIDKey(insPrevID)] {
			pbNode.InsPrevId = actors.nodeID(insPrevID)
		}
		emitted[nodeIDKey(node.ID())] = true
		pbTree.Nodes = append(pbTree.Nodes, pbNode)
	}
	// Purge order depends on GC iteration, so the entries go out sorted (the
	// JS encoder sorts the same way): one state, one snapshot.
	type purgedEntry struct {
		createdAt *time.Ticket
		entry     *crdt.PurgedLedger
	}
	var purged []purgedEntry
	tree.RGATreeSplit().PurgedLedgers(func(createdAt *time.Ticket, entries []*crdt.PurgedLedger) {
		for _, e := range entries {
			purged = append(purged, purgedEntry{createdAt, e})
		}
	})
	sort.SliceStable(purged, func(i, j int) bool {
		a, b := purged[i], purged[j]
		if c := a.createdAt.Compare(b.createdAt); c != 0 {
			return c < 0
		}
		if a.entry.Start != b.entry.Start {
			return a.entry.Start < b.entry.Start
		}
		if a.entry.End != b.entry.End {
			return a.entry.End < b.entry.End
		}
		for k := 0; k < min(len(a.entry.Dels), len(b.entry.Dels)); k++ {
			if c := a.entry.Dels[k].Compare(b.entry.Dels[k]); c != 0 {
				return c < 0
			}
		}
		return len(a.entry.Dels) < len(b.entry.Dels)
	})
	for _, p := range purged {
		pbEntry := &api.ZeroPurgedLedger{
			CreatedAt: ToTimeTicket(p.createdAt),
			Start:     int32(p.entry.Start),
			End:       int32(p.entry.End),
		}
		for _, d := range p.entry.Dels {
			pbEntry.Dels = append(pbEntry.Dels, ToTimeTicket(d))
		}
		pbTree.PurgedLedger = append(pbTree.PurgedLedger, pbEntry)
	}
	return &api.JSONElement{Body: &api.JSONElement_ZeroTree_{ZeroTree: pbTree}}
}

func nodeIDKey(id *crdt.RGATreeSplitNodeID) string {
	return fmt.Sprintf("%s:%d", id.CreatedAt().Key(), id.Offset())
}

func fromJSONZeroTree(pbTree *api.JSONElement_ZeroTree) (*crdt.ZeroTree, error) {
	createdAt, err := fromRequiredTimeTicket(pbTree.CreatedAt, "json_zero_tree.created_at")
	if err != nil {
		return nil, err
	}
	movedAt, err := fromTimeTicket(pbTree.MovedAt)
	if err != nil {
		return nil, err
	}
	removedAt, err := fromTimeTicket(pbTree.RemovedAt)
	if err != nil {
		return nil, err
	}

	rga := crdt.NewRGATreeSplit(crdt.InitialZeroTreeNode())
	decoded := make(map[string]*crdt.RGATreeSplitNode[*crdt.ZeroTokens])
	current := rga.InitialHead()
	actors := &zeroActors{index: map[string]uint32{}}
	for i, b := range pbTree.Actors {
		actor, err := time.ActorIDFromBytes(b)
		if err != nil {
			return nil, err
		}
		actors.ids = append(actors.ids, actor)
		actors.index[actor.String()] = uint32(i)
	}
	for _, pbNode := range pbTree.Nodes {
		id, err := actors.fromNodeID(pbNode.Id)
		if err != nil {
			return nil, err
		}
		if id == nil {
			return nil, goerrors.New("zero tree node id missing")
		}
		value, err := fromZeroTokens(pbNode.Value, actors)
		if err != nil {
			return nil, err
		}
		node := crdt.NewRGATreeSplitNode(id, value)
		nodeRemovedAt, err := actors.fromTicket(pbNode.RemovedAt)
		if err != nil {
			return nil, err
		}
		node.SetRemovedAt(nodeRemovedAt)
		if pbNode.HasLedger {
			dels := make([]*time.Ticket, 0, len(pbNode.Dels))
			for _, d := range pbNode.Dels {
				ticket, err := actors.fromRequired(d, "zero_tree_node.dels")
				if err != nil {
					return nil, err
				}
				dels = append(dels, ticket)
			}
			cancels := make([]crdt.LedgerCancel, 0, len(pbNode.Cancels))
			for _, c := range pbNode.Cancels {
				deletion, err := actors.fromRequired(c.Deletion, "zero_cancel.deletion")
				if err != nil {
					return nil, err
				}
				at, err := actors.fromRequired(c.CancelledAt, "zero_cancel.cancelled_at")
				if err != nil {
					return nil, err
				}
				cancels = append(cancels, crdt.LedgerCancel{Deletion: deletion, CancelledAt: at})
			}
			if len(dels) == 0 {
				dels = nil
			}
			if len(cancels) == 0 {
				cancels = nil
			}
			node.SetLedger(dels, cancels)
		}
		current = rga.InsertAfter(current, node)
		if pbNode.InsPrevId != nil {
			insPrevID, err := actors.fromNodeID(pbNode.InsPrevId)
			if err != nil {
				return nil, err
			}
			insPrev, ok := decoded[nodeIDKey(insPrevID)]
			if !ok || insPrevID.CreatedAt().Compare(id.CreatedAt()) != 0 || insPrevID.Offset() >= id.Offset() {
				return nil, goerrors.New("zero tree insPrev must name an earlier piece of the same insertion")
			}
			current.SetInsPrev(insPrev)
		}
		decoded[nodeIDKey(id)] = current
	}
	for _, e := range pbTree.PurgedLedger {
		ticket, err := fromRequiredTimeTicket(e.CreatedAt, "zero_purged_ledger.created_at")
		if err != nil {
			return nil, err
		}
		entry := &crdt.PurgedLedger{Start: int(e.Start), End: int(e.End)}
		for _, d := range e.Dels {
			del, err := fromRequiredTimeTicket(d, "zero_purged_ledger.dels")
			if err != nil {
				return nil, err
			}
			entry.Dels = append(entry.Dels, del)
		}
		rga.AddPurgedLedger(ticket, entry)
	}

	tree := crdt.NewZeroTree(rga, createdAt)
	tree.SetSchema(fromZeroSchema(pbTree.Schema))
	tree.SetMovedAt(movedAt)
	tree.SetRemovedAt(removedAt)
	return tree, nil
}

func toSchemaChildren(list []crdt.ZeroSchemaChildren) []*api.ZeroSchemaChildren {
	out := make([]*api.ZeroSchemaChildren, 0, len(list))
	for _, e := range list {
		out = append(out, &api.ZeroSchemaChildren{Type: e.Type, Children: e.Children})
	}
	return out
}

func fromSchemaChildren(list []*api.ZeroSchemaChildren) []crdt.ZeroSchemaChildren {
	out := make([]crdt.ZeroSchemaChildren, 0, len(list))
	for _, e := range list {
		out = append(out, crdt.ZeroSchemaChildren{Type: e.Type, Children: e.Children})
	}
	return out
}

// toZeroSchema / fromZeroSchema convert the schema a ZeroTree carries, order
// kept (it decides which child fills a gap).
func toZeroSchema(s *crdt.ZeroSchema) *api.ZeroSchema {
	if s == nil {
		return nil
	}
	pb := &api.ZeroSchema{
		TextType:     s.TextType,
		TextTypes:    s.TextTypes,
		Scoped:       s.Scoped,
		Single:       toSchemaChildren(s.Single),
		RootType:     s.RootType,
		Atomic:       s.Atomic,
		DefaultChild: toSchemaChildren(s.DefaultChild),
		Allowed:      toSchemaChildren(s.Allowed),
	}
	if s.Grid != nil {
		pb.Grid = &api.ZeroSchemaGrid{Table: s.Grid.Table, Row: s.Grid.Row, Cell: s.Grid.Cell}
	}
	return pb
}

func fromZeroSchema(pb *api.ZeroSchema) *crdt.ZeroSchema {
	if pb == nil {
		return nil
	}
	s := &crdt.ZeroSchema{
		TextType:     pb.TextType,
		TextTypes:    pb.TextTypes,
		Scoped:       pb.Scoped,
		Single:       fromSchemaChildren(pb.Single),
		RootType:     pb.RootType,
		Atomic:       pb.Atomic,
		DefaultChild: fromSchemaChildren(pb.DefaultChild),
		Allowed:      fromSchemaChildren(pb.Allowed),
	}
	if pb.Grid != nil {
		s.Grid = &crdt.ZeroSchemaGrid{Table: pb.Grid.Table, Row: pb.Grid.Row, Cell: pb.Grid.Cell}
	}
	return s
}

// zeroTreeToBytes and bytesToZeroTree carry a ZeroTree inside
// JSONElementSimple (a Set of a new ZeroTree), as TreeToBytes does.
func zeroTreeToBytes(tree *crdt.ZeroTree) ([]byte, error) {
	bytes, err := proto.Marshal(toZeroTree(tree))
	if err != nil {
		return nil, fmt.Errorf("marshal zero tree: %w", err)
	}
	return bytes, nil
}

func bytesToZeroTree(bytes []byte) (*crdt.ZeroTree, error) {
	pbElem := &api.JSONElement{}
	if err := proto.Unmarshal(bytes, pbElem); err != nil {
		return nil, fmt.Errorf("unmarshal zero tree: %w", err)
	}
	pbTree, ok := pbElem.Body.(*api.JSONElement_ZeroTree_)
	if !ok {
		return nil, ErrUnsupportedElement
	}
	return fromJSONZeroTree(pbTree.ZeroTree)
}
