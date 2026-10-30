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

package operations

// PROTOTYPE -- the operations of crdt.ZeroTree. They mirror the JS SDK's
// zero_tree_operation.ts so a server replaying a change log builds the same
// tokens, tombstones and removal ledgers as the clients. The Zero editor
// operations a client reports to its binding are computed client side only.

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ZeroTreeEdit deletes [from, to) and inserts content, or -- with restore
// spans -- revives / re-removes token runs by identity (undo / redo).
type ZeroTreeEdit struct {
	parentCreatedAt  *time.Ticket
	from             *crdt.RGATreeSplitNodePos
	to               *crdt.RGATreeSplitNodePos
	content          *crdt.ZeroTokens
	executedAt       *time.Ticket
	restoreSpans     []*crdt.ZeroRestoreSpan
	restoreMode      crdt.RestoreMode
	retombstoneSpans []*crdt.ZeroRestoreSpan
}

// NewZeroTreeEdit creates a new instance of ZeroTreeEdit.
func NewZeroTreeEdit(
	parentCreatedAt *time.Ticket,
	from, to *crdt.RGATreeSplitNodePos,
	content *crdt.ZeroTokens,
	executedAt *time.Ticket,
	restoreSpans []*crdt.ZeroRestoreSpan,
	restoreMode crdt.RestoreMode,
	retombstoneSpans []*crdt.ZeroRestoreSpan,
) *ZeroTreeEdit {
	return &ZeroTreeEdit{
		parentCreatedAt:  parentCreatedAt,
		from:             from,
		to:               to,
		content:          content,
		executedAt:       executedAt,
		restoreSpans:     restoreSpans,
		restoreMode:      restoreMode,
		retombstoneSpans: retombstoneSpans,
	}
}

func validateZeroRestoreIdentities(spans []*crdt.ZeroRestoreSpan, vv time.VersionVector) error {
	createdAts := make([]*time.Ticket, 0, len(spans))
	for _, span := range spans {
		createdAts = append(createdAts, span.CreatedAt)
	}
	return validateRestoreTickets(createdAts, vv)
}

func withZeroRemovedBy(spans []*crdt.ZeroRestoreSpan, removedBy *time.Ticket) []*crdt.ZeroRestoreSpan {
	out := make([]*crdt.ZeroRestoreSpan, len(spans))
	for i, s := range spans {
		cp := *s
		cp.RemovedBy = removedBy
		out[i] = &cp
	}
	return out
}

// Execute executes this operation on the given ZeroTree.
//
//nolint:gocyclo // PROTOTYPE: mirrors ZeroTreeEditOperation.execute in the JS SDK
func (e *ZeroTreeEdit) Execute(root *crdt.Root, source OpSource, vv time.VersionVector) (ExecutionResult, error) {
	tree, ok := root.FindByCreatedAt(e.parentCreatedAt).(*crdt.ZeroTree)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}

	if e.restoreMode != crdt.RestoreModeNone || len(e.restoreSpans) > 0 || len(e.retombstoneSpans) > 0 {
		if err := validateZeroRestoreIdentities(e.restoreSpans, vv); err != nil {
			return ExecutionResult{}, err
		}
		if err := validateZeroRestoreIdentities(e.retombstoneSpans, vv); err != nil {
			return ExecutionResult{}, err
		}
		isRetombstone := e.restoreMode == crdt.RestoreModeRetombstone
		toRestore, toRetombstone := e.restoreSpans, e.retombstoneSpans
		if isRetombstone {
			toRestore, toRetombstone = e.retombstoneSpans, e.restoreSpans
		}

		observable := false
		if len(toRetombstone) > 0 {
			pairs, diff, err := tree.Retombstone(toRetombstone, e.executedAt, vv)
			if err != nil {
				return ExecutionResult{}, err
			}
			observable = observable || len(pairs) > 0
			root.Acc(diff)
			for _, pair := range pairs {
				root.RegisterGCPair(pair)
			}
		}
		if len(toRestore) > 0 {
			untombstoned, recreated, stillTombstoned, err := tree.Restore(toRestore, e.executedAt, e.from)
			if err != nil {
				return ExecutionResult{}, err
			}
			observable = observable || len(untombstoned) > 0 || len(recreated) > 0
			for _, pair := range stillTombstoned {
				root.RegisterGCPair(pair)
			}
			var diff resource.DataSize
			for _, node := range untombstoned {
				root.UnregisterGCPair(crdt.GCPair{Parent: tree.RGATreeSplit(), Child: node})
				diff.Add(node.DataSize())
			}
			for _, node := range recreated {
				diff.Add(node.DataSize())
			}
			root.Acc(diff)
		}

		if !source.NeedsReverse() {
			return ExecutionResult{Observable: observable}, nil
		}
		// The spans this execution removed again are restored by the next
		// reverse, which has to cancel THIS removal.
		restoreSpans, retombstoneSpans := e.restoreSpans, withZeroRemovedBy(e.retombstoneSpans, e.executedAt)
		mode := crdt.RestoreModeRetombstone
		if isRetombstone {
			restoreSpans, retombstoneSpans = withZeroRemovedBy(e.restoreSpans, e.executedAt), e.retombstoneSpans
			mode = crdt.RestoreModeRestore
		}
		return ExecutionResult{
			Reverse: NewZeroTreeEdit(e.parentCreatedAt, e.from, e.to, nil, nil,
				restoreSpans, mode, retombstoneSpans),
			Observable: observable,
		}, nil
	}

	var content *crdt.ZeroTokens
	if e.content != nil {
		content = e.content.DeepCopy().(*crdt.ZeroTokens)
	}
	pairs, diff, removedSpans, err := tree.Edit(e.from, e.to, content, e.executedAt, vv)
	root.Acc(diff)
	for _, pair := range pairs {
		root.RegisterGCPair(pair)
	}
	if err != nil {
		return ExecutionResult{}, err
	}
	observable := (e.content != nil && e.content.Len() > 0) || len(removedSpans) > 0
	if !source.NeedsReverse() {
		return ExecutionResult{Observable: observable}, nil
	}

	var inserted []*crdt.ZeroRestoreSpan
	if e.content != nil && e.content.Len() > 0 {
		inserted = []*crdt.ZeroRestoreSpan{{
			CreatedAt: e.executedAt,
			Start:     0,
			End:       e.content.Len(),
			Value:     e.content.DeepCopy().(*crdt.ZeroTokens),
		}}
	}
	if len(removedSpans) == 0 && inserted == nil {
		return ExecutionResult{Observable: observable}, nil
	}
	anchor, err := tree.NormalizePos(e.from)
	if err != nil {
		return ExecutionResult{}, err
	}
	return ExecutionResult{
		Reverse: NewZeroTreeEdit(e.parentCreatedAt, anchor, anchor, nil, nil,
			removedSpans, crdt.RestoreModeRestore, inserted),
		Observable: observable,
	}, nil
}

// ExecutedAt returns execution time of this operation.
func (e *ZeroTreeEdit) ExecutedAt() *time.Ticket { return e.executedAt }

// SetActor sets the given actor to this operation.
func (e *ZeroTreeEdit) SetActor(actorID time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (e *ZeroTreeEdit) SetExecutedAt(executedAt *time.Ticket) { e.executedAt = executedAt }

// ParentCreatedAt returns the creation time of the ZeroTree.
func (e *ZeroTreeEdit) ParentCreatedAt() *time.Ticket { return e.parentCreatedAt }

// From returns the start of the range.
func (e *ZeroTreeEdit) From() *crdt.RGATreeSplitNodePos { return e.from }

// To returns the end of the range.
func (e *ZeroTreeEdit) To() *crdt.RGATreeSplitNodePos { return e.to }

// Content returns the inserted tokens (nil: none).
func (e *ZeroTreeEdit) Content() *crdt.ZeroTokens { return e.content }

// RestoreSpans returns the spans an undo revives.
func (e *ZeroTreeEdit) RestoreSpans() []*crdt.ZeroRestoreSpan { return e.restoreSpans }

// RestoreMode returns the restore direction.
func (e *ZeroTreeEdit) RestoreMode() crdt.RestoreMode { return e.restoreMode }

// RetombstoneSpans returns the spans an undo re-removes.
func (e *ZeroTreeEdit) RetombstoneSpans() []*crdt.ZeroRestoreSpan { return e.retombstoneSpans }

// ZeroTreeStyle writes attributes on one marker with last-writer-wins. Its
// reverse cancels the write (see crdt.ZeroStyleCancels) and writes back the
// reserved keys; the reverse of that brings the write back.
type ZeroTreeStyle struct {
	parentCreatedAt *time.Ticket
	markerID        *crdt.RGATreeSplitNodeID
	writes          []crdt.ZeroAttrWrite
	executedAt      *time.Ticket
	// rng makes the write a range (see crdt.ZeroStyleRegion).
	rng *crdt.ZeroStyleRange
	// cancel cancels or brings back an earlier write (an undo or redo).
	cancel *crdt.ZeroCancelWrite
}

// NewZeroTreeStyle creates a new instance of ZeroTreeStyle.
func NewZeroTreeStyle(
	parentCreatedAt *time.Ticket,
	markerID *crdt.RGATreeSplitNodeID,
	writes []crdt.ZeroAttrWrite,
	executedAt *time.Ticket,
	rng *crdt.ZeroStyleRange,
	cancel *crdt.ZeroCancelWrite,
) *ZeroTreeStyle {
	return &ZeroTreeStyle{
		parentCreatedAt: parentCreatedAt,
		markerID:        markerID,
		writes:          writes,
		executedAt:      executedAt,
		rng:             rng,
		cancel:          cancel,
	}
}

// Execute executes this operation on the given ZeroTree.
func (e *ZeroTreeStyle) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (ExecutionResult, error) {
	tree, ok := root.FindByCreatedAt(e.parentCreatedAt).(*crdt.ZeroTree)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}
	prev, pairs, cancel := tree.SetAttrs(e.markerID, e.writes, e.executedAt, e.rng, e.cancel)
	for _, pair := range pairs {
		root.RegisterGCPair(pair)
	}
	// a range write may change other markers even when its own kept a newer
	// value, and so may a cancel
	observable := len(prev) > 0 || e.rng != nil || cancel != nil
	if !source.NeedsReverse() || (len(prev) == 0 && cancel == nil) {
		return ExecutionResult{Observable: observable}, nil
	}
	var rng *crdt.ZeroStyleRange
	if len(prev) > 0 {
		rng = e.rng
	}
	return ExecutionResult{
		Reverse:    NewZeroTreeStyle(e.parentCreatedAt, e.markerID, prev, nil, rng, cancel),
		Observable: true,
	}, nil
}

// ExecutedAt returns execution time of this operation.
func (e *ZeroTreeStyle) ExecutedAt() *time.Ticket { return e.executedAt }

// SetActor sets the given actor to this operation.
func (e *ZeroTreeStyle) SetActor(actorID time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (e *ZeroTreeStyle) SetExecutedAt(executedAt *time.Ticket) { e.executedAt = executedAt }

// ParentCreatedAt returns the creation time of the ZeroTree.
func (e *ZeroTreeStyle) ParentCreatedAt() *time.Ticket { return e.parentCreatedAt }

// MarkerID returns the id of the marker this operation writes on.
func (e *ZeroTreeStyle) MarkerID() *crdt.RGATreeSplitNodeID { return e.markerID }

// Writes returns the attribute writes.
func (e *ZeroTreeStyle) Writes() []crdt.ZeroAttrWrite { return e.writes }

// Range returns the range of a range write, or nil.
func (e *ZeroTreeStyle) Range() *crdt.ZeroStyleRange { return e.rng }

// Cancel returns the cancel of an undo or redo of a write, or nil.
func (e *ZeroTreeStyle) Cancel() *crdt.ZeroCancelWrite { return e.cancel }
