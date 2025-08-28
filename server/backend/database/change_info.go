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

 package database

 import (
	"errors"
	 "fmt"
 
	 "google.golang.org/protobuf/proto"
 
	 "github.com/yorkie-team/yorkie/api/converter"
	 "github.com/yorkie-team/yorkie/api/types"
	 api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	 "github.com/yorkie-team/yorkie/pkg/document/change"
	 "github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	 "github.com/yorkie-team/yorkie/pkg/document/time"
 )

 var ErrCombineChanges = errors.New("combine changes failed")
 
 // ChangeInfo is a structure representing information of an change.
 type ChangeInfo struct {
	 ID            types.ID           `bson:"_id"`
	 ProjectID     types.ID           `bson:"project_id"`
	 DocID         types.ID           `bson:"doc_id"`
	 OpSeq         int64              `bson:"op_seq"`
	 PrSeq         int64              `bson:"pr_seq"`
	 ClientSeq     uint32             `bson:"client_seq"`
	 Lamport       int64              `bson:"lamport"`
	 ActorID       types.ID           `bson:"actor_id"`
	 VersionVector time.VersionVector `bson:"version_vector"`
	 Message       string             `bson:"message"`
	 Operations    [][]byte           `bson:"operations"`
	 PresenceChange *innerpresence.Change `bson:"presence_change"`
 }
 
 // NewChangeInfo creates a new ChangeInfo from the given change and sequences.
 func NewChangeInfo(docKey types.DocRefKey, c *change.Change, opSeq, prSeq int64) (*ChangeInfo, error) {
	 if c == nil {
		 return nil, fmt.Errorf("change cannot be nil")
	 }
 
	 encodedOperations, err := EncodeOperations(c.Operations())
	 if err != nil {
		 return nil, fmt.Errorf("encode operations: %w", err)
	 }
 
	 return &ChangeInfo{
		 ProjectID:     docKey.ProjectID,
		 DocID:         docKey.DocID,
		 OpSeq:         opSeq,
		 PrSeq:         prSeq,
		 ClientSeq:     c.ClientSeq(),
		 Lamport:       c.ID().Lamport(),
		 ActorID:       types.ID(c.ID().ActorID().String()),
		 VersionVector: c.ID().VersionVector(),
		 Message:       c.Message(),
		 Operations:    encodedOperations,
		 PresenceChange: c.PresenceChange(),
	 }, nil
 }
 
 // ToChange creates Change model from this ChangeInfo.
 func (i *ChangeInfo) ToChange() (*change.Change, error) {
	actorID, err := time.ActorIDFromHex(i.ActorID.String())
	if err != nil {
		return nil, err
	}

	serverSeq := change.NewServerSeq(i.OpSeq, i.PrSeq)
	changeID := change.NewID(i.ClientSeq, serverSeq, i.Lamport, actorID, i.VersionVector)

	var pbOps []*api.Operation
	for _, bytesOp := range i.Operations {
		pbOp := api.Operation{}
		if err := proto.Unmarshal(bytesOp, &pbOp); err != nil {
			return nil, ErrDecodeOperationFailed
		}
		pbOps = append(pbOps, &pbOp)
	}

	ops, err := converter.FromOperations(pbOps)
	if err != nil {
		return nil, err
	}

	c := change.New(changeID, i.Message, ops, i.PresenceChange)
	c.SetServerSeq(change.NewServerSeq(i.OpSeq, i.PrSeq))

	return c, nil
 }

// CombineChangeInfos combines operation and presence change infos into unified change infos.
func CombineChangeInfos(
	operationInfos []*OperationChangeInfo,
	presenceInfos []*PresenceChangeInfo,
) ([]*ChangeInfo, error) {
	var changeInfos []*ChangeInfo
	opIdx := 0
	prIdx := 0

	// Two-pointer algorithm to merge operation and presence change infos
	for opIdx < len(operationInfos) || prIdx < len(presenceInfos) {
		var opInfo *OperationChangeInfo
		var prInfo *PresenceChangeInfo

		// Determine which pointer to advance based on {opSeq, prSeq} comparison
		if opIdx >= len(operationInfos) {
			// Only presence infos remain
			prInfo = presenceInfos[prIdx]
			prIdx++
		} else if prIdx >= len(presenceInfos) {
			// Only operation infos remain
			opInfo = operationInfos[opIdx]
			opIdx++
		} else {
			// Compare {opSeq, prSeq} pairs to decide which pointer to advance
			opCurrent := operationInfos[opIdx]
			prCurrent := presenceInfos[prIdx]

			// Compare directly without creating ServerSeq objects
			if opCurrent.OpSeq == prCurrent.OpSeq && opCurrent.PrSeq == prCurrent.PrSeq {
				// Same {opSeq, prSeq} pair, combine into one change
				opInfo = opCurrent
				prInfo = prCurrent
				opIdx++
				prIdx++
			} else if (opCurrent.OpSeq < prCurrent.OpSeq) || 
					  (opCurrent.OpSeq == prCurrent.OpSeq && opCurrent.PrSeq < prCurrent.PrSeq) {
				// Operation comes first
				opInfo = opCurrent
				opIdx++
			} else {
				// Presence comes first
				prInfo = prCurrent
				prIdx++
			}
		}

		// Create the combined change info directly
		combinedChangeInfo, err := createCombinedChangeInfo(opInfo, prInfo)
		if err != nil {
			return nil, fmt.Errorf("create combined change info: %w", err)
		}

		changeInfos = append(changeInfos, combinedChangeInfo)
	}

	return changeInfos, nil
}

// createCombinedChangeInfo creates a ChangeInfo from operation and/or presence info
func createCombinedChangeInfo(
	opInfo *OperationChangeInfo,
	prInfo *PresenceChangeInfo,
) (*ChangeInfo, error) {
	if opInfo != nil && prInfo != nil {
		// Both operation and presence, combine them into one ChangeInfo
		return &ChangeInfo{
			ProjectID:      opInfo.ProjectID,
			DocID:          opInfo.DocID,
			OpSeq:          opInfo.OpSeq,
			PrSeq:          opInfo.PrSeq,
			ClientSeq:      opInfo.ClientSeq,
			Lamport:        opInfo.Lamport,
			ActorID:        opInfo.ActorID,
			VersionVector:  opInfo.VersionVector,
			Message:        opInfo.Message,
			Operations:     opInfo.Operations,
			PresenceChange: prInfo.PresenceChange,
		}, nil
		
	} else if opInfo != nil {
		// Only operation: convert OperationChangeInfo to ChangeInfo
		return &ChangeInfo{
			ProjectID:      opInfo.ProjectID,
			DocID:          opInfo.DocID,
			OpSeq:          opInfo.OpSeq,
			PrSeq:          opInfo.PrSeq,
			ClientSeq:      opInfo.ClientSeq,
			Lamport:        opInfo.Lamport,
			ActorID:        opInfo.ActorID,
			VersionVector:  opInfo.VersionVector,
			Message:        opInfo.Message,
			Operations:     opInfo.Operations,
			PresenceChange: nil,
		}, nil
		
	} else if prInfo != nil {
		// Only presence: convert PresenceChangeInfo to ChangeInfo
		return &ChangeInfo{
			ProjectID:      prInfo.ProjectID,
			DocID:          prInfo.DocID,
			OpSeq:          prInfo.OpSeq,
			PrSeq:          prInfo.PrSeq,
			ClientSeq:      prInfo.ClientSeq,
			Lamport:        time.InitialLamport,
			ActorID:        prInfo.ActorID,
			VersionVector:  time.InitialVersionVector,
			Message:        prInfo.Message,
			Operations:     nil,
			PresenceChange: prInfo.PresenceChange,
		}, nil
		
	} else {
		return nil, ErrCombineChanges
	}
}
 
 // DeepCopy returns a deep copy of this ChangeInfo.
 func (i *ChangeInfo) DeepCopy() *ChangeInfo {
	 if i == nil {
		 return nil
	 }
 
	 clone := &ChangeInfo{}
	 *clone = *i
 
	 return clone
 }
 
 
 