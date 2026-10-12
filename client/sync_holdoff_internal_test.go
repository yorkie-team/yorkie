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

package client

import (
	"errors"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/key"
)

const (
	testRetryDelay    = 50 * gotime.Millisecond
	testRejectedDelay = 10 * gotime.Second
)

// serverError builds the error a connect client sees for the given server-side
// code: a connect.Error carrying an ErrorInfo detail, which is what
// converter.ErrorCodeOf reads. Built here rather than through the server's
// connecthelper so this in-package test does not depend on the server.
func serverError(t *testing.T, code string) error {
	t.Helper()

	err := connect.NewError(connect.CodeInvalidArgument, errors.New(code))
	detail, detailErr := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Metadata: map[string]string{"code": code},
	})
	require.NoError(t, detailErr)
	err.AddDetail(detail)
	return err
}

// docAttachment returns a realtime document attachment with both reasons to
// sync pending: a local change to push and a remote change to pull.
func docAttachment(t *testing.T, k key.Key) *Attachment {
	t.Helper()

	doc := document.New(k)
	doc.SetStatus(document.StatusAttached)
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "v")
		return nil
	}))

	a := &Attachment{resource: doc, syncMode: SyncModeRealtime}
	a.changeEventReceived.Store(true)
	return a
}

// TestRejectedPushHoldsOffTheAttachment pins what the sync loop does with a
// document whose push the server refused: it stops resending the pack for
// RejectedPushRetryDelay -- which also stops it pulling, since push and pull
// share one PushPull -- and then re-probes, rather than waiting for an
// explicit Sync that an app driven by the watch stream alone never makes.
func TestRejectedPushHoldsOffTheAttachment(t *testing.T) {
	rejected := serverError(t, document.ErrDocumentSizeExceedsLimit.Code())

	t.Run("held off, and pulls nothing while it is", func(t *testing.T) {
		a := docAttachment(t, key.Key("rejected-holdoff"))
		require.True(t, a.needSync(0))

		a.recordSync(rejected, true, testRetryDelay, testRejectedDelay)

		// Both reasons to sync are still pending, including a remote change
		// this client has been told about and has not pulled.
		assert.True(t, a.changeEventReceived.Load())
		assert.False(t, a.needSync(0), "a refused document is still synced")
	})

	t.Run("re-probes once the delay has passed", func(t *testing.T) {
		a := docAttachment(t, key.Key("rejected-reprobe"))

		// A delay short enough to pass within the test: the loop must pick
		// the document up again by itself, with no explicit Sync.
		a.recordSync(rejected, true, gotime.Hour, 30*gotime.Millisecond)
		require.False(t, a.needSync(0))

		assert.Eventually(t, func() bool { return a.needSync(0) },
			gotime.Second, 5*gotime.Millisecond,
			"a refused document never syncs again by itself")
	})

	t.Run("a success puts it straight back in the loop", func(t *testing.T) {
		a := docAttachment(t, key.Key("rejected-cleared"))
		a.recordSync(rejected, true, testRetryDelay, testRejectedDelay)
		require.False(t, a.needSync(0))

		a.recordSync(nil, true, testRetryDelay, testRejectedDelay)
		assert.True(t, a.needSync(0))
	})
}

// TestFailedExplicitSyncDoesNotHoldOff pins that a failed Client.Sync leaves
// the attachment alone: its caller has the error and decides what to do, and
// the loop keeps syncing the document at its own cadence. Both failures the
// loop treats differently are checked, since only the loop's own are its to
// act on.
func TestFailedExplicitSyncDoesNotHoldOff(t *testing.T) {
	for name, err := range map[string]error{
		"refused push":    serverError(t, document.ErrDocumentSizeExceedsLimit.Code()),
		"transient error": connect.NewError(connect.CodeUnavailable, errors.New("restarting")),
	} {
		t.Run(name, func(t *testing.T) {
			a := docAttachment(t, key.Key("explicit-"+name))

			a.recordSync(err, false, testRetryDelay, testRejectedDelay)

			assert.True(t, a.retryAt.IsZero(), "an explicit Sync held the attachment off")
			assert.True(t, a.needSync(0), "an explicit Sync's failure stopped the loop")
		})
	}
}

// TestFailedLoopSyncUsesRetryDelay pins that any other failure of the loop's
// gets the short delay, not the long one a refused push gets: the document is
// retried after RetrySyncLoopDelay rather than every round.
func TestFailedLoopSyncUsesRetryDelay(t *testing.T) {
	t.Run("document", func(t *testing.T) {
		a := docAttachment(t, key.Key("transient-doc"))

		before := gotime.Now()
		a.recordSync(connect.NewError(connect.CodeUnavailable, errors.New("restarting")),
			true, testRetryDelay, testRejectedDelay)

		assert.False(t, a.needSync(0))
		assert.WithinDuration(t, before.Add(testRetryDelay), a.retryAt, testRetryDelay)
		assert.Eventually(t, func() bool { return a.needSync(0) },
			gotime.Second, 5*gotime.Millisecond)
	})

	// A channel has no push to refuse, so the refused-push code from some
	// other call must not buy it the long delay.
	t.Run("channel", func(t *testing.T) {
		ch, err := channel.New(key.Key("transient-channel"))
		require.NoError(t, err)
		a := &Attachment{resource: ch, syncMode: SyncModeRealtime}

		before := gotime.Now()
		a.recordSync(serverError(t, document.ErrDocumentSizeExceedsLimit.Code()),
			true, testRetryDelay, testRejectedDelay)

		assert.WithinDuration(t, before.Add(testRetryDelay), a.retryAt, testRetryDelay)
		assert.Eventually(t, func() bool { return a.needSync(0) },
			gotime.Second, 5*gotime.Millisecond)
	})
}
