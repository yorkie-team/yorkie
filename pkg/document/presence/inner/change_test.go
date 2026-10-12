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

package inner_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestPatch(t *testing.T) {
	patch := &inner.Change{
		ChangeType:  inner.Patch,
		Presence:    inner.Presence{"cursor": "2", "tool": "pen"},
		RemovedKeys: []string{"selection"},
	}

	t.Run("ApplyTo sets and removes keys without touching the base", func(t *testing.T) {
		base := inner.Presence{"name": "a", "cursor": "1", "selection": "x"}

		merged := patch.ApplyTo(base)
		assert.Equal(t, inner.Presence{"name": "a", "cursor": "2", "tool": "pen"}, merged)
		assert.Equal(t, inner.Presence{"name": "a", "cursor": "1", "selection": "x"}, base)
	})

	t.Run("ApplyTo treats a nil base as empty", func(t *testing.T) {
		assert.Equal(t, inner.Presence{"cursor": "2", "tool": "pen"}, patch.ApplyTo(nil))
	})

	t.Run("Execute merges a patch into the stored presence", func(t *testing.T) {
		presences := inner.NewMap()
		presences.Store(time.InitialActorID.String(), inner.Presence{"name": "a", "selection": "x"})

		patch.Execute(time.InitialActorID, presences)
		assert.Equal(t,
			inner.Presence{"name": "a", "cursor": "2", "tool": "pen"},
			presences.Load(time.InitialActorID.String()),
		)
	})
}
