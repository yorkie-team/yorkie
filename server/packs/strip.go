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

import "github.com/yorkie-team/yorkie/pkg/document/change"

// stripPresenceChanges removes the PresenceChange from each change for a
// document configured with disable_presence. A change carrying only a
// presence update drops in full; a mixed change keeps its operations and
// loses its presence. The input slice is reused as the output backing
// array, so the function is allocation-free.
func stripPresenceChanges(changes []*change.Change) []*change.Change {
	if len(changes) == 0 {
		return changes
	}

	out := changes[:0]
	for _, c := range changes {
		if c.PresenceChange() != nil {
			if !c.HasOperations() {
				continue
			}
			c.SetPresenceChange(nil)
		}
		out = append(out, c)
	}
	return out
}
