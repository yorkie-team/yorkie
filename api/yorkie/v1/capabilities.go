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

// Package v1 provides the Yorkie v1 API: the protobuf-generated types for the
// wire format, and the hand-written constants that describe it.
package v1

import "slices"

// Wire capabilities are the names carried in ChangePack.capabilities. They live
// beside the generated code rather than in the server configuration so that a
// capability is versioned with the wire format it describes: a build that can
// decode a feature is exactly the build that advertises it.
//
// The names are part of the wire contract. Never rename or reuse one; a peer
// matches on the string. ServerCapabilities is append-only for the same reason
// once a server has emitted it, and removing an emitted entry is a breaking
// change even though no proto field moves.
const (
	// CapElementRestore means the peer understands RestoreMode on element
	// operations and the revived_at register on elements, so an undo of a
	// removal can revive the element already in the tree instead of inserting
	// a copy of it under ids the document is already indexed by.
	CapElementRestore = "element-restore"

	// CapPresencePatch means the server folds PresenceChange CHANGE_TYPE_PATCH
	// into a full put before storing it, so a client may send only the
	// presence keys that changed. A server without it does not reject a patch
	// but stores it as an empty presence, so a client that gets a pack without
	// this capability must stop patching and resend its full presence. See
	// docs/design/presence-patch.md.
	CapPresencePatch = "presence-patch"
)

// ServerCapabilities is what a server advertises on every ChangePack it
// returns. Append only, and only for what the server honours: the converter
// does not decode RestoreMode on element operations or revived_at yet, so
// CapElementRestore is left out until it does.
var ServerCapabilities = []string{
	CapPresencePatch,
}

// HasCapability reports whether the given capability list contains name.
//
// Absence means unsupported, not unknown. A server that predates a capability
// cannot report that it lacks it, and a change pack it re-encodes drops fields
// it does not know while still succeeding, so a caller that treats an empty
// list as "probably fine" would emit operations that are silently downgraded on
// the peer and never stored.
func HasCapability(capabilities []string, name string) bool {
	return slices.Contains(capabilities, name)
}
