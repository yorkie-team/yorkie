/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ErrEncodeOperationFailed is returned when encoding operations failed.
var ErrEncodeOperationFailed = errors.New("encode operations failed")

// ErrDecodeOperationFailed is returned when decoding operations failed.
var ErrDecodeOperationFailed = errors.New("decode operations failed")

// ChangeInfo is a structure representing information of a change.
type ChangeInfo struct {
	ID             types.ID `bson:"_id"`
	ProjectID      types.ID `bson:"project_id"`
	DocID          types.ID `bson:"doc_id"`
	ServerSeq      int64    `bson:"server_seq"`
	ClientSeq      uint32   `bson:"client_seq"`
	Lamport        int64    `bson:"lamport"`
	ActorID        types.ID `bson:"actor_id"`
	Message        string   `bson:"message"`
	Operations     [][]byte `bson:"operations"`
	PresenceChange string   `bson:"presence_change"`
}

// EncodeOperations encodes the given operations into bytes array.
func EncodeOperations(operations []operations.Operation) ([][]byte, error) {
	var encodedOps [][]byte

	changes, err := converter.ToOperations(operations)
	if err != nil {
		return nil, err
	}

	for _, pbOp := range changes {
		encodedOp, err := proto.Marshal(pbOp)
		if err != nil {
			return nil, ErrEncodeOperationFailed
		}
		encodedOps = append(encodedOps, encodedOp)
	}

	return encodedOps, nil
}

// EncodePresenceChange encodes the given presence change into string.
func EncodePresenceChange(p *innerpresence.PresenceChange) (string, error) {
	if p == nil {
		return "", nil
	}

	bytes, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal presence change to bytes: %w", err)
	}

	return string(bytes), nil
}

// ToChange creates Change model from this ChangeInfo.
func (i *ChangeInfo) ToChange() (*change.Change, error) {
	actorID, err := time.ActorIDFromHex(i.ActorID.String())
	if err != nil {
		return nil, err
	}

	changeID := change.NewID(i.ClientSeq, i.ServerSeq, i.Lamport, actorID)

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

	p, err := innerpresence.NewChangeFromJSON(i.PresenceChange)
	if err != nil {
		return nil, err
	}

	c := change.New(changeID, i.Message, ops, p)
	c.SetServerSeq(i.ServerSeq)

	return c, nil
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
