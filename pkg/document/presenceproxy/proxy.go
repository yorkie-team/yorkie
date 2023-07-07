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

// Package presenceproxy provides the proxy for the InternalPresence to be manipulated from the outside.
// TODO(hackerwins): Consider to remove this package. It is used to solve the problem of cyclic dependency
// between pkg/document/presence and pkg/document/change.
package presenceproxy

import (
	"github.com/yorkie-team/yorkie/pkg/document/change"
	presence2 "github.com/yorkie-team/yorkie/pkg/document/presence"
)

// Presence represents a proxy for the InternalPresence to be manipulated from the outside.
type Presence struct {
	presence *presence2.InternalPresence
	context  *change.Context
}

// New creates a new instance of Presence.
func New(ctx *change.Context, presence *presence2.InternalPresence) *Presence {
	return &Presence{
		presence: presence,
		context:  ctx,
	}
}

// Set sets the value of the given key.
func (p *Presence) Set(key string, value string) {
	internalPresence := *p.presence
	internalPresence.Set(key, value)
	p.context.SetPresence(internalPresence)
}
