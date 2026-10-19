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

package document

import (
	"testing"
	gotime "time"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestGarbageCollectDuringAnotherUpdateReadsNoClock pins the lock-free path
// of GarbageCollect: while another goroutine's updater holds the lock, the
// collection runs without it, and must not read the document's clock, which
// that Update writes as its change lands. Under -race, a read of it there
// is a data race.
func TestGarbageCollectDuringAnotherUpdateReadsNoClock(t *testing.T) {
	d := New("gc-during-update")
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{{Type: "p"}}})
		return nil
	}))
	inside := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-inside
		d.GarbageCollect(time.NewVersionVector())
		close(done)
	}()
	require.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
		close(inside)
		// Long enough for the collection to run while this updater holds
		// the lock, with nothing ordering it before the write below.
		gotime.Sleep(100 * gotime.Millisecond)
		root.SetString("x", "v")
		return nil
	}))
	<-done
}
