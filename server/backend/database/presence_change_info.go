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
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// PresenceChangeInfo represents a presence change stored in memory.
type PresenceChangeInfo struct {
	ProjectID types.ID
	DocID     types.ID
	OpSeq     int64
	PrSeq     int64
	ClientSeq uint32
	ActorID   types.ID
	PresenceChange *innerpresence.Change
	Message string
}

// NewPresenceChangeInfo creates a new PresenceChangeInfo from a change and sequences.
func NewPresenceChangeInfo(docKey types.DocRefKey, c *change.Change, opSeq, prSeq int64) *PresenceChangeInfo {
	if c == nil || c.PresenceChange() == nil {
		return nil
	}

	return &PresenceChangeInfo{
		ProjectID:      docKey.ProjectID,
		DocID:          docKey.DocID,
		OpSeq:          opSeq,
		PrSeq:          prSeq,
		ClientSeq:      c.ClientSeq(),
		ActorID:        types.ID(c.ID().ActorID().String()),
		PresenceChange: c.PresenceChange(),
		Message:        c.Message(),
	}
}

// ToChange creates a partial Change from this PresenceChangeInfo (only presence part).
func (p *PresenceChangeInfo) ToChange() (*change.Change, error) {
	actorID, err := time.ActorIDFromHex(p.ActorID.String())
	if err != nil {
		return nil, err
	}

	// Create ID with both opSeq and prSeq for reassembly
	// This is a presence-only change without logical clocks
	changeID := change.NewID(
		p.ClientSeq,
		change.NewServerSeq(p.OpSeq, p.PrSeq), // Use both sequences for reassembly
		time.InitialLamport,                   // Use initial lamport since presence doesn't need logical clocks
		actorID,
		time.NewVersionVector(),               // Use empty version vector since presence doesn't need logical clocks
	)

	// Create change with no operations, only presence
	c := change.New(changeID, p.Message, nil, p.PresenceChange)

	return c, nil
}

// DeepCopy returns a deep copy of this PresenceChangeInfo.
func (p *PresenceChangeInfo) DeepCopy() *PresenceChangeInfo {
	if p == nil {
		return nil
	}

	clone := &PresenceChangeInfo{}
	*clone = *p

	// Deep copy presence change
	if p.PresenceChange != nil {
		clone.PresenceChange = &innerpresence.Change{
			ChangeType: p.PresenceChange.ChangeType,
			Presence:   p.PresenceChange.Presence.DeepCopy(),
		}
	}

	return clone
}
