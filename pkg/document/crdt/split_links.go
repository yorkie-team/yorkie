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

// DropSplitLinksInElement strips the split-sibling links from every tree
// reachable from elem.
//
// A Set/Add/SetByIndex payload carries a whole element rather than a position
// in the document, and the wire format carries InsPrevID/InsNextID on every
// tree node it holds. The tree follows them as trusted structural pointers, so
// drop them here for the same reason FromTreeNodesWhenEdit drops them from
// operation content.
//
// The merge lineage is NOT dropped. An element payload is not always freshly
// created content: the reverse of a Remove carries a DeepCopy of the element
// as it stood, so its MergedFrom/MergedAt can be the genuine record of a merge
// that happened before the removal. Clearing them would leave the restored
// tree without the §1.1 insert redirect and the §6.2 propagation skip that
// read them. Only DropEngineOnlyLinks, which runs on TreeEdit content no merge
// can have touched, clears those.
//
// Both ways into the document have to agree: the converter calls this on the
// element bytes it decodes, and executeUndoRedo calls it on the copy a reverse
// operation captured, which never passes the converter on the replica that
// runs the undo. Removed members are walked too -- they are still registered
// in NodeMapByID.
func DropSplitLinksInElement(elem Element) {
	switch e := elem.(type) {
	case *Tree:
		if root := e.Root(); root != nil {
			root.DropSplitLinks()
		}
	case *Object:
		for _, node := range e.RHTNodes() {
			DropSplitLinksInElement(node.Element())
		}
	case *Array:
		for _, node := range e.AllRGANodes() {
			DropSplitLinksInElement(node.Element())
		}
	}
}
