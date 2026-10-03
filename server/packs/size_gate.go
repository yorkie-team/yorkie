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
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/server/backend"
)

// canGrow reports whether the change may increase the document's live size.
// The push path has no document to execute the change against, so this goes
// by operation kind alone and is conservative: only removals are known not to
// grow. A change with no operations (presence-only) cannot grow the root.
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
			if o.Content() == "" && len(o.Attributes()) == 0 && len(o.RestoreSpans()) == 0 {
				continue
			}
		case *operations.TreeEdit:
			if len(o.Contents()) == 0 && o.SplitLevel() == 0 && len(o.RestoreSpans()) == 0 {
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
	if info.LiveSize <= int64(maxSize) {
		return nil
	}

	return fmt.Errorf(
		"push to %s: live size %d bytes at server seq %d exceeds %d: %w",
		docKey, info.LiveSize, info.ServerSeq, maxSize, document.ErrDocumentSizeExceedsLimit,
	)
}
