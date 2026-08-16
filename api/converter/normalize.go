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

import (
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// NormalizeStoredOperations repairs, in place, the two shapes FromOperations
// rejects that a change persisted before those checks existed could still
// carry. It exists because FromOperations sits on two paths with opposite
// requirements: it decodes operations arriving from a client, where rejecting
// malformed input is the point, and it also decodes operations read back from
// storage (ChangeInfo.ToChange), where rejecting anything makes the document
// holding that change permanently unloadable. Validation added at the wire
// boundary therefore applies retroactively to data already written under the
// looser rules, and only the stored side calls this.
//
// Whether any such change exists cannot be answered by a query -- operations
// are persisted as opaque protobuf blobs -- so the population is unknown
// rather than known-empty. Normalizing costs nothing if it is empty and
// avoids an unrecoverable read failure if it is not.
//
// Only TreeEdit is touched because both checks live on it. Each repair
// restores what the field meant before the check, except where that meaning
// was itself a crash; see the individual comments.
func NormalizeStoredOperations(pbOps []*api.Operation) {
	for _, pbOp := range pbOps {
		pbTreeEdit := pbOp.GetTreeEdit()
		if pbTreeEdit == nil {
			continue
		}

		// A negative split level was inert before it was rejected: nothing
		// read it beyond the split loop, which does nothing for a
		// non-positive level. Clamping to zero reproduces that exactly, and
		// keeps the level from sizing an inverted range in the
		// boundary-deletion reverse that now reads it.
		if pbTreeEdit.SplitLevel < 0 {
			pbTreeEdit.SplitLevel = 0
		}

		dropUndatedAttrs(pbTreeEdit.RestoreSpans)
		dropUndatedAttrs(pbTreeEdit.RetombstoneSpans)
	}
}

// dropUndatedAttrs removes restore-span attributes carrying no updatedAt.
//
// This one deliberately does not reproduce the prior behavior: fromRHT stores
// whatever fromTimeTicket returns, which is nil for a nil ticket rather than
// an error, so such an attribute used to reach the RHT and panic on the first
// comparison deep inside the restore path. Dropping the attribute loses one
// style entry on a span; keeping it loses the server. An attribute with no
// updatedAt cannot participate in RHT's last-writer-wins resolution anyway,
// which is what makes dropping it the closest thing to a meaning it has.
func dropUndatedAttrs(pbSpans []*api.TreeRestoreSpan) {
	for _, pbSpan := range pbSpans {
		if pbSpan == nil {
			continue
		}

		for key, attr := range pbSpan.Attributes {
			if attr == nil || attr.UpdatedAt == nil {
				delete(pbSpan.Attributes, key)
			}
		}
	}
}
