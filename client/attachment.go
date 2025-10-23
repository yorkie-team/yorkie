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
	watchStream      <-chan WatchDocResponse

	syncMu              sync.RWMutex
	syncMode            SyncMode
	changeEventReceived bool
	lastSyncTime        gotime.Time
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
			(doc.HasLocalChanges() || a.changeEventReceived)
	}

	if a.syncMode == SyncModeManual {
		return false
	}
	return gotime.Since(a.lastSyncTime) >= heartbeatInterval
}
