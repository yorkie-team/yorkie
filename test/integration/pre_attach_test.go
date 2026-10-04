//go:build integration

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

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestPreAttachEdits covers documents edited before they are attached. Their
// elements are created under the initial actor, so two clients that fill the
// same key used to push values with the same createdAt; the attach now
// re-issues those tickets to each client's own actor.
func TestPreAttachEdits(t *testing.T) {
	t.Run("two clients filling the same key before attach converge", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestKey(t)

		var last string
		var written []string
		for round := range 3 {
			clients := activeClients(t, 2)
			docs := []*document.Document{document.New(docKey), document.New(docKey)}
			contents := []string{
				fmt.Sprintf("round%d-c1", round),
				fmt.Sprintf("round%d-c2", round),
			}
			written = append(written, contents...)
			for i, doc := range docs {
				require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
					r.SetNewText("k1").Edit(0, 0, contents[i])
					r.SetNewObject("o").SetString("by", contents[i])
					return nil
				}))
			}

			wg := sync.WaitGroup{}
			for i := range docs {
				wg.Go(func() { assert.NoError(t, clients[i].Attach(ctx, docs[i])) })
			}
			wg.Wait()

			syncClientsThenAssertEqual(t, []clientAndDocPair{
				{clients[0], docs[0]},
				{clients[1], docs[1]},
			})

			// One whole client's write wins every key, never a mix or nothing.
			// Every pre-attach write has lamport 1, so an earlier round's
			// value may still win by the actor tie-break.
			text := docs[0].Root().GetText("k1").String()
			assert.Contains(t, written, text, docs[0].Marshal())
			assert.Equal(t, fmt.Sprintf(`{"by":"%s"}`, text), docs[0].Root().GetObject("o").Marshal())
			last = docs[0].Marshal()

			deactivateAndCloseClients(t, clients)
		}

		observers := activeClients(t, 1)
		defer deactivateAndCloseClients(t, observers)
		observed := document.New(docKey)
		require.NoError(t, observers[0].Attach(ctx, observed))
		assert.Equal(t, last, observed.Marshal())
	})
}
