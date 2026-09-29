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

package document_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestSyncAccessorsLockDuringConcurrentUpdate covers the client's sync loop,
// which calls HasLocalChanges and CreateChangePack on a goroutine of its own
// while the application may be inside Update appending to localChanges. The
// d.updating escape is per-document, so these two must not take it: under
// -race, an unlocked read here reports a data race against Update's append.
func TestSyncAccessorsLockDuringConcurrentUpdate(t *testing.T) {
	doc := document.New("d")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				_ = doc.HasLocalChanges()
				_ = doc.CreateChangePack()
			}
		}
	})

	for i := range 1000 {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("k", i)
			return nil
		}))
	}

	close(stop)
	wg.Wait()
	require.True(t, doc.HasLocalChanges())
}

// TestAttachSettersLockDuringConcurrentUpdate covers the client's attach and
// detach paths, which set the actor, the status, the size limit and the
// schema rules while another goroutine may be inside Update reading them.
// Like the sync loop's accessors, these setters run beside an updater, never
// inside one, so they must take d.mu rather than the d.updating escape.
func TestAttachSettersLockDuringConcurrentUpdate(t *testing.T) {
	doc := document.New("d")
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				doc.SetActor(actor)
				doc.SetStatus(document.StatusAttached)
				doc.SetMaxSizeLimit(1 << 20)
				doc.SetSchemaRules(nil)
			}
		}
	})

	for i := range 1000 {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("k", i)
			return nil
		}))
	}

	close(stop)
	wg.Wait()
}
