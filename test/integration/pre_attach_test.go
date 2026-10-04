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
	"github.com/yorkie-team/yorkie/pkg/document/time"
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

		var last, lastText string
		for round := range 3 {
			clients := activeClients(t, 2)
			docs := []*document.Document{document.New(docKey), document.New(docKey)}
			contents := []string{
				fmt.Sprintf("round%d-c1", round),
				fmt.Sprintf("round%d-c2", round),
			}
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
			// Every pre-attach write has lamport 1, so the previous round's
			// winner may still win by the actor tie-break -- but nothing older.
			text := docs[0].Root().GetText("k1").String()
			candidates := append([]string{}, contents...)
			if lastText != "" {
				candidates = append(candidates, lastText)
			}
			assert.Contains(t, candidates, text, docs[0].Marshal())
			lastText = text
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

	t.Run("attach re-issues pre-attach tickets to the client's actor", func(t *testing.T) {
		ctx := context.Background()
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		doc := document.New(helper.TestKey(t))
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("k1").Edit(0, 0, "abc")
			return nil
		}))
		require.Equal(t, time.InitialActorID, doc.RootObject().Get("k1").CreatedAt().ActorID())
		require.True(t, doc.CanUndo())

		// A failed attach keeps the re-issued state: the document is valid as
		// is, and the outcome of a failed RPC is not always known.
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		assert.Error(t, clients[0].Attach(canceled, doc))
		assert.Equal(t, clients[0].ID(), doc.ActorID())
		assert.Equal(t, clients[0].ID(), doc.RootObject().Get("k1").CreatedAt().ActorID())
		assert.False(t, doc.CanUndo())

		// A retry under another client re-issues from that actor to its own.
		require.NoError(t, clients[1].Attach(ctx, doc))
		assert.Equal(t, clients[1].ID(), doc.ActorID())
		assert.Equal(t, clients[1].ID(), doc.RootObject().Get("k1").CreatedAt().ActorID())
		assert.Equal(t, `{"k1":[{"val":"abc"}]}`, doc.Marshal())

		observer := document.New(doc.Key())
		require.NoError(t, clients[0].Attach(ctx, observer))
		assert.Equal(t, doc.Marshal(), observer.Marshal())
		assert.Equal(t, clients[1].ID(), observer.RootObject().Get("k1").CreatedAt().ActorID())
	})

	t.Run("a second pre-attach document of the same key is not re-issued", func(t *testing.T) {
		ctx := context.Background()
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		cli := clients[0]
		docKey := helper.TestKey(t)

		first := document.New(docKey)
		require.NoError(t, first.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("first").Edit(0, 0, "a")
			return nil
		}))
		require.NoError(t, cli.Attach(ctx, first))
		require.Equal(t, cli.ID(), first.RootObject().Get("first").CreatedAt().ActorID())
		require.NoError(t, cli.Detach(ctx, first))

		// A re-issue leaves the lamports as they are, and a fresh document
		// starts them at 1, so re-issuing this one to the same actor would
		// mint the createdAt the first attach already pushed. The tickets stay
		// under the initial actor instead.
		second := document.New(docKey)
		require.NoError(t, second.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("second").Edit(0, 0, "b")
			return nil
		}))
		require.NoError(t, cli.Attach(ctx, second))
		assert.Equal(t, time.InitialActorID, second.RootObject().Get("second").CreatedAt().ActorID())

		// Both elements survive: the server can still tell them apart.
		assert.Equal(t, "a", second.Root().GetText("first").String())
		assert.Equal(t, "b", second.Root().GetText("second").String())
	})
}
