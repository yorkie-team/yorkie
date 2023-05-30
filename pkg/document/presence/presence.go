/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package presence

// Presence represents custom presence that can be defined by the client.
// If the client is watching a document, the presence is shared with
// all other clients watching the same document.
type Presence map[string]string

// PresenceInfo is a presence information with logical clock.
type PresenceInfo struct {
	Clock    int32
	Presence Presence
}

// Update updates the given presence information with the given clock.
func (i *PresenceInfo) Update(info PresenceInfo) bool {
	if info.Clock > i.Clock {
		i.Clock = info.Clock
		i.Presence = info.Presence
		return true
	}
	return false
}
