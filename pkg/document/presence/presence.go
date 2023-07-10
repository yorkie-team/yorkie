/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package presence provides the proxy for the innerpresence.Presence to be manipulated from the outside.
// TODO(hackerwins): Consider to remove this package. It is used to solve the problem of cyclic dependency
// between pkg/document/presence and pkg/document/change.
package presence

import (
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
)

// Presence represents a proxy for the Presence to be manipulated from the outside.
type Presence struct {
	presence *innerpresence.Presence
	context  *change.Context
}

// New creates a new instance of Presence.
func New(ctx *change.Context, presence *innerpresence.Presence) *Presence {
	return &Presence{
		presence: presence,
		context:  ctx,
	}
}

// Set sets the value of the given key.
func (p *Presence) Set(key string, value string) {
	internalPresence := *p.presence
	internalPresence.Set(key, value)

	// TODO(hackerwins): We should support partial update here.
	p.context.SetPresenceChange(innerpresence.PresenceChange{
		ChangeType: "put",
		Presence:   internalPresence,
	})
}
