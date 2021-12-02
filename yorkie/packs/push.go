/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package packs

import (
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

// pushChanges returns the changes excluding already saved in DB.
func pushChanges(
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	pack *change.Pack,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	cp := clientInfo.Checkpoint(docInfo.ID)

	var pushedChanges []*change.Change
	for _, cn := range pack.Changes {
		if cn.ID().ClientSeq() > cp.ClientSeq {
			serverSeq := docInfo.IncreaseServerSeq()
			cp = cp.NextServerSeq(serverSeq)
			cn.SetServerSeq(serverSeq)
			pushedChanges = append(pushedChanges, cn)
		} else {
			log.Logger.Warnf("change is rejected: %d vs %d ", cn.ID().ClientSeq(), cp.ClientSeq)
		}

		cp = cp.SyncClientSeq(cn.ClientSeq())
	}

	if len(pack.Changes) > 0 {
		log.Logger.Infof(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.ID,
			len(pushedChanges),
			docInfo.Key,
			len(pack.Changes)-len(pushedChanges),
			initialServerSeq,
			docInfo.ServerSeq,
			cp.String(),
		)
	}

	return cp, pushedChanges, nil
}
