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
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// ErrDocumentSizeExceedsLimit is returned when a push would grow a document
// that is already past the project's MaxSizePerDocument. It is returned as a
// StatusError rather than a pre-wrapped connect error so connecthelper
// attaches the custom code as ErrorInfo metadata, giving the SDK the same
// machine-readable name its local check uses.
var ErrDocumentSizeExceedsLimit = errors.ResourceExhausted(
	"document size exceeds the limit",
).WithCode("ErrDocumentSizeExceedsLimit")

// checkDocSize refuses a push that could grow a document already past the
// project's MaxSizePerDocument. It is the server-side backstop for a quota
// that the SDK also checks locally in Document.Update; without it the quota is
// advisory against anything but a stock client.
//
// Two properties keep it from refusing an honest client:
//
//   - The size it reads is DocInfo.DocSize, written by the snapshot path, so
//     it lags the real document by at most one snapshot interval and is zero
//     for a document never snapshotted. The gate is therefore strictly looser
//     than the client's exact check, which stays the primary one.
//   - It only refuses changes that can grow the document. DocSize.Total()
//     counts GC bytes as well as live ones, so a deletion does not shrink it;
//     refusing deletions too would leave an over-quota document with no push
//     the server would accept at all.
func checkDocSize(
	project *types.Project,
	docInfo *database.DocInfo,
	changes []*change.Change,
) error {
	limit := int64(project.MaxSizePerDocument)
	if limit <= 0 || docInfo.DocSize <= limit {
		return nil
	}

	for _, cn := range changes {
		if mayGrowDocument(cn) {
			return ErrDocumentSizeExceedsLimit
		}
	}

	return nil
}

// mayGrowDocument reports whether the given change could increase the
// document's size.
//
// The push path has no root to execute the change against, so the answer is
// drawn from the operation kind and its payload alone and errs toward "yes":
// anything that is not plainly a removal counts as growth. A change carrying
// no operations — a presence-only change — cannot touch the root and is not
// growth.
func mayGrowDocument(cn *change.Change) bool {
	for _, op := range cn.Operations() {
		switch o := op.(type) {
		case *operations.Remove:
			// Moves the element's bytes from Live to GC, leaving Total alone.
		case *operations.Edit:
			// A pure deletion carries no content and no attributes. Restore
			// spans revive tombstoned characters, which is a move out of GC
			// rather than growth, but they are refused here anyway: reviving
			// content on an over-quota document is not a way back under it.
			if o.Content() != "" || len(o.Attributes()) > 0 ||
				len(o.RestoreSpans()) > 0 || len(o.RetombstoneSpans()) > 0 {
				return true
			}
		case *operations.TreeEdit:
			// Same shape as Edit. A non-zero split level mints new nodes, so
			// it is growth even with no contents.
			if len(o.Contents()) > 0 || o.SplitLevel() > 0 ||
				len(o.RestoreSpans()) > 0 || len(o.RetombstoneSpans()) > 0 {
				return true
			}
		default:
			// Set, Add, Move, Increase, ArraySet, Style, TreeStyle and
			// anything added later.
			return true
		}
	}

	return false
}
