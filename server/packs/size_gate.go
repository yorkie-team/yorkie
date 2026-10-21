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

package packs

import (
	"context"
	"fmt"
	"slices"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
)

// canGrow reports whether the change may increase the document's size.
// The push path has no document to execute the change against, so this goes
// by operation kind alone and is conservative: only removals are known not to
// grow. A change with no operations (presence-only) cannot grow the root.
//
// An identity-preserving edit revives one of its two span sets and removes
// the other, and its restore mode says which: a retombstone-mode edit revives
// its retombstone spans (see TreeEdit.Execute and Edit.Execute). It can grow
// the document when the set its mode revives is not empty.
//
// A deletion can still add a little node metadata where it splits a text or
// tree node at the range boundary. That growth is bounded by the content
// already in the document, since each position can be deleted only once.
func canGrow(cn *change.Change) bool {
	for _, op := range cn.Operations() {
		switch o := op.(type) {
		case *operations.Remove:
			continue
		case *operations.Edit:
			revived := len(o.RestoreSpans())
			if o.RestoreMode() == crdt.RestoreModeRetombstone {
				revived = len(o.RetombstoneSpans())
			}
			if o.Content() == "" && len(o.Attributes()) == 0 && revived == 0 {
				continue
			}
		case *operations.TreeEdit:
			revived := len(o.RestoreSpans())
			if o.RestoreMode() == crdt.RestoreModeRetombstone {
				revived = len(o.RetombstoneSpans())
			}
			if len(o.Contents()) == 0 && o.SplitLevel() == 0 && revived == 0 {
				continue
			}
		}
		return true
	}
	return false
}

// checkDocumentSize enforces the project's MaxSizePerDocument on a push.
//
// The size comes from the latest snapshot, so it lags the document by up to
// one SnapshotInterval of changes; see docs/design/document-size-limit.md.
// It is what the document holds, Live and GC, the figure the SDK's own
// MaxSizeLimit check reads, so growth a removed element books to GC is
// bounded too.
// A document without a measured size (no snapshot yet, a snapshot written
// before sizes were recorded, or one just compacted) is admitted. Only a
// document strictly over the limit refuses, and only changes that can grow
// it: an over-quota document must stay shrinkable, or it deadlocks.
func checkDocumentSize(
	ctx context.Context,
	be *backend.Backend,
	docKey types.DocRefKey,
	serverSeq int64,
	maxSize int,
	changes []*change.Change,
) error {
	if maxSize <= 0 {
		return nil
	}

	if !slices.ContainsFunc(changes, canGrow) {
		return nil
	}

	info, err := be.DB.FindClosestSnapshotInfo(ctx, docKey, serverSeq, false)
	if err != nil {
		return err
	}
	if info.Size <= int64(maxSize) {
		return nil
	}

	return fmt.Errorf(
		"push to %s: size %d bytes at server seq %d exceeds %d: %w",
		docKey, info.Size, info.ServerSeq, maxSize, document.ErrDocumentSizeExceedsLimit,
	)
}

// CheckLiveSize enforces the project's MaxSizePerDocument against a document
// the caller already holds in full, on its live size.
//
// The server-side write paths that build the whole root themselves — the admin
// document create and update RPCs and the revision restore — measure the
// result exactly, so they use this instead of the snapshot-based push gate.
// That gate refuses every growing change on an over-quota document, which
// would refuse the very writes meant to bring such a document back under the
// limit; measuring the result admits a shrink and still refuses an overrun.
//
// It reads Live, not the total the push gate reads. Two of the paths replace
// the whole root, which leaves every byte of the previous content in GC,
// uncollected, at the moment they measure: on the total, a repair that
// shrinks an over-quota document would be refused for the garbage the repair
// itself booked. The result's Live is the content the repair wrote, and its
// garbage is collected like any other.
func CheckLiveSize(docKey key.Key, size resource.DocSize, maxSize int) error {
	liveSize := size.Live.Total()
	if maxSize <= 0 || liveSize <= maxSize {
		return nil
	}

	return fmt.Errorf(
		"update %s: live size %d bytes exceeds %d: %w",
		docKey, liveSize, maxSize, document.ErrDocumentSizeExceedsLimit,
	)
}
