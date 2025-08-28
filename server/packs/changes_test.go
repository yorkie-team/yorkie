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

 package packs_test

 import (
	 "testing"
 
	 "github.com/stretchr/testify/assert"
 
	 "github.com/yorkie-team/yorkie/api/types"
	 "github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	 "github.com/yorkie-team/yorkie/pkg/document/time"
	 "github.com/yorkie-team/yorkie/server/backend/database"
	 "github.com/yorkie-team/yorkie/server/packs"
 )
 
 func TestCombineChangeInfosSimple(t *testing.T) {
	 t.Run("combine operation only", func(t *testing.T) {
		 opInfo := &database.OperationChangeInfo{
			 ProjectID:     types.ID("project1"),
			 DocID:         types.ID("doc1"),
			 OpSeq:         1,
			 PrSeq:         0,
			 ClientSeq:     10,
			 Lamport:       100,
			 ActorID:       types.ID("actor1"),
			 VersionVector: time.NewVersionVector(),
			 Message:       "test operation",
			 Operations:    [][]byte{[]byte(`{"type":"set","path":"key","value":"value"}`)},
		 }
 
		 changeInfos, err := packs.CombineChangeInfos([]*database.OperationChangeInfo{opInfo}, []*database.PresenceChangeInfo{})
		 assert.NoError(t, err)
		 assert.Len(t, changeInfos, 1)
 
		 changeInfo := changeInfos[0]
		 assert.Equal(t, int64(1), changeInfo.OpSeq)
		 assert.Equal(t, int64(0), changeInfo.PrSeq)
		 assert.Equal(t, uint32(10), changeInfo.ClientSeq)
		 assert.NotNil(t, changeInfo.Operations)
		 assert.Nil(t, changeInfo.PresenceChange)
	 })
 
	 t.Run("combine presence only", func(t *testing.T) {
		 prInfo := &database.PresenceChangeInfo{
			 ProjectID:      types.ID("project1"),
			 DocID:          types.ID("doc1"),
			 OpSeq:          0,
			 PrSeq:          1,
			 ClientSeq:      10,
			 ActorID:        types.ID("actor1"),
			 PresenceChange: &innerpresence.Change{Type: innerpresence.Put},
			 Message:        "test presence",
		 }
 
		 changeInfos, err := packs.CombineChangeInfos([]*database.OperationChangeInfo{}, []*database.PresenceChangeInfo{prInfo})
		 assert.NoError(t, err)
		 assert.Len(t, changeInfos, 1)
 
		 changeInfo := changeInfos[0]
		 assert.Equal(t, int64(0), changeInfo.OpSeq)
		 assert.Equal(t, int64(1), changeInfo.PrSeq)
		 assert.Equal(t, uint32(10), changeInfo.ClientSeq)
		 assert.Nil(t, changeInfo.Operations)
		 assert.NotNil(t, changeInfo.PresenceChange)
	 })
 
	 t.Run("empty arrays", func(t *testing.T) {
		 changeInfos, err := packs.CombineChangeInfos([]*database.OperationChangeInfo{}, []*database.PresenceChangeInfo{})
		 assert.NoError(t, err)
		 assert.Len(t, changeInfos, 0)
	 })
 }
 