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
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// ServerPack is similar to change.Pack, but has ChangeInfos instead of Changes
// to reduce type conversion in Server.
type ServerPack struct {
	// DocumentKey is key of the document.
	DocumentKey key.Key

	// Checkpoint is used to determine the client received changes.
	Checkpoint change.Checkpoint

	// ChangeInfos represents a unit of modification in the document.
	ChangeInfos []*database.ChangeInfo

	// Snapshot is a byte array that encode the document.
	Snapshot []byte

	// SnapshotVersionVector is the version vector of the snapshot.
	SnapshotVersionVector time.VersionVector

	// MinSyncedVersionVector is the minimum version vector of the client who attached the document.
	MinSyncedVersionVector time.VersionVector

	// MinSyncedTicket is the minimum logical time taken by clients who attach the document.
	// It used to collect garbage on the replica on the client.
	MinSyncedTicket *time.Ticket

	// IsRemoved is a flag that indicates whether the document is removed.
	IsRemoved bool
}

// NewServerPack creates a new instance of ServerPack.
func NewServerPack(
	key key.Key,
	cp change.Checkpoint,
	changeInfos []*database.ChangeInfo,
	snapshot []byte,
) *ServerPack {
	return &ServerPack{
		DocumentKey: key,
		Checkpoint:  cp,
		ChangeInfos: changeInfos,
		Snapshot:    snapshot,
	}
}

// ChangesLen returns the size of the changes.
func (p *ServerPack) ChangesLen() int {
	return len(p.ChangeInfos)
}

// OperationsLen returns the size of the operations.
func (p *ServerPack) OperationsLen() int {
	ops := 0
	for _, info := range p.ChangeInfos {
		ops += len(info.Operations)
	}
	return ops
}

// SnapshotLen returns the size of the snapshot.
func (p *ServerPack) SnapshotLen() int {
	return len(p.Snapshot)
}

// ToPBChangePack converts the given model format to Protobuf format.
func (p *ServerPack) ToPBChangePack() (*api.ChangePack, error) {
	var pbChanges []*api.Change
	for _, info := range p.ChangeInfos {
		actorID, err := time.ActorIDFromHex(info.ActorID.String())
		if err != nil {
			return nil, err
		}

		changeID := change.NewID(info.ClientSeq, info.ServerSeq, info.Lamport, actorID, info.VersionVector)

		var pbOps []*api.Operation
		for _, bytesOp := range info.Operations {
			pbOp := api.Operation{}
			if err := proto.Unmarshal(bytesOp, &pbOp); err != nil {
				return nil, database.ErrDecodeOperationFailed
			}
			pbOps = append(pbOps, &pbOp)
		}

		p, err := innerpresence.NewChangeFromJSON(info.PresenceChange)
		if err != nil {
			return nil, err
		}

		pbChangeID, err := converter.ToChangeID(changeID)
		if err != nil {
			return nil, err
		}

		pbChanges = append(pbChanges, &api.Change{
			Id:             pbChangeID,
			Message:        info.Message,
			Operations:     pbOps,
			PresenceChange: converter.ToPresenceChange(p),
		})
	}

	pbPack := &api.ChangePack{
		DocumentKey:     p.DocumentKey.String(),
		Checkpoint:      converter.ToCheckpoint(p.Checkpoint),
		Changes:         pbChanges,
		MinSyncedTicket: converter.ToTimeTicket(p.MinSyncedTicket),
		IsRemoved:       p.IsRemoved,
	}

	if p.MinSyncedVersionVector != nil {
		pbMinSyncedVersionVector, err := converter.ToVersionVector(p.MinSyncedVersionVector)
		if err != nil {
			return nil, err
		}

		pbPack.MinSyncedVersionVector = pbMinSyncedVersionVector
	}

	if p.Snapshot != nil {
		pbVersionVector, err := converter.ToVersionVector(p.SnapshotVersionVector)
		if err != nil {
			return nil, err
		}

		pbPack.Snapshot = p.Snapshot
		pbPack.SnapshotVersionVector = pbVersionVector
	}

	return pbPack, nil
}

// ApplyDocInfo applies the given DocInfo to the ServerPack.
func (p *ServerPack) ApplyDocInfo(info *database.DocInfo) {
	if info.IsRemoved() {
		p.IsRemoved = true
	}
}
