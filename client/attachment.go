/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"context"
	"sync"
	"sync/atomic"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document"
)

// SyncMode defines the synchronization mode for resources.
type SyncMode string

const (
	// SyncModeManual indicates that changes are not automatically pushed or pulled.
	SyncModeManual SyncMode = "manual"

	// SyncModeRealtime indicates that changes are automatically pushed and pulled.
	SyncModeRealtime SyncMode = "realtime"

	// SyncModeRealtimePushOnly indicates that only local changes are automatically pushed.
	SyncModeRealtimePushOnly SyncMode = "realtime-pushonly"

	// SyncModeRealtimeSyncOff indicates that changes are not automatically pushed or pulled,
	// but the watch stream is kept active.
	SyncModeRealtimeSyncOff SyncMode = "realtime-syncoff"
)

// Attachment represents the document attached.
type Attachment struct {
	resourceID types.ID
	resource   attachable.Attachable

	watchCtx         context.Context
	closeWatchStream context.CancelFunc

	// watchStream, watchBuf, watchPumpStop and watchPumpDone form the watch
	// delivery pipeline. It belongs to the attachment rather than to a single
	// runWatchLoop invocation: the event pump and the sender live until the
	// pipeline is stopped, so a loop re-establishing its stream never leaves
	// Document.Events without a consumer, and watchStream is written once,
	// before the attachment is published, rather than rewritten from the
	// reconnecting goroutine. All four are immutable after that point.
	//
	// The pump stops on watchPumpStop rather than on watchCtx because it has
	// to outlive every producer of document events, and the stream readers
	// keep publishing presence reconciliations for as long as they take to
	// notice the cancelled watchCtx. See stopWatchPipeline.
	watchStream   <-chan WatchDocResponse
	watchBuf      *watchBuffer
	watchPumpStop chan struct{}
	watchPumpDone chan struct{}

	// watchReaders counts the stream reader goroutines runWatchLoop has
	// started and not yet finished. A reconnect adds the new reader before
	// the old one returns, so the counter never drops to zero mid-handover.
	watchReaders sync.WaitGroup

	// watchStopOnce guards the teardown so a second caller -- Deactivate
	// walking an attachment that a concurrent Detach is tearing down, say --
	// waits for the first teardown instead of closing watchPumpStop twice.
	watchStopOnce sync.Once

	syncMu       sync.RWMutex
	syncMode     SyncMode
	lastSyncTime gotime.Time

	// changeEventReceived records that the watch stream reported a remote
	// change that this client has not pulled yet. It is atomic rather than
	// guarded by syncMu because the stream reader sets it: a reader that
	// takes syncMu would deadlock against a teardown that holds syncMu and
	// waits for the readers to exit.
	changeEventReceived atomic.Bool

	// disableGC is set when the document was attached with
	// WithDisableGC. The client sets the matching wire field on every
	// PushPullChanges so the server can skip minVV tracking and omit the
	// response VersionVector. See docs/design/disable-gc-on-attach.md.
	disableGC bool

	// disablePresence carries the server-fixated DisablePresence value
	// returned by AttachDocument. It is purely informational on the client
	// today (the local gating is held on the Document itself); kept here so
	// future RPCs that need the fixated value can read it without going
	// back to the Document.
	disablePresence bool
}

func (a *Attachment) Is(resourceType attachable.ResourceType) bool {
	return a.resource.Type() == resourceType
}

// needSync determines if the attachment needs sync.
func (a *Attachment) needSync(heartbeatInterval gotime.Duration) bool {
	a.syncMu.RLock()
	defer a.syncMu.RUnlock()

	if a.resource.Type() == attachable.TypeDocument {
		doc, ok := a.resource.(*document.Document)
		if !ok {
			return false
		}

		if a.syncMode == SyncModeRealtimeSyncOff {
			return false
		}

		if a.syncMode == SyncModeRealtimePushOnly {
			return doc.HasLocalChanges()
		}

		return a.syncMode != SyncModeManual &&
			(doc.HasLocalChanges() || a.changeEventReceived.Load())
	}

	if a.syncMode == SyncModeManual {
		return false
	}
	return gotime.Since(a.lastSyncTime) >= heartbeatInterval
}
