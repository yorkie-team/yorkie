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

package memory_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/database/testcases"
)

const (
	projectID    = types.ID("000000000000000000000000")
	projectOneID = types.ID("000000000000000000000001")
	projectTwoID = types.ID("000000000000000000000002")
)

func TestDB(t *testing.T) {
	db, err := memory.New()
	assert.NoError(t, err)

	t.Run("RunLeadership Test", func(t *testing.T) {
		testcases.RunLeadershipTest(t, db)
	})

	t.Run("FindNextNCyclingProjectInfos test", func(t *testing.T) {
		testcases.RunFindNextNCyclingProjectInfosTest(t, db)
	})

	t.Run("FindDeactivateCandidatesPerProject test", func(t *testing.T) {
		testcases.RunFindDeactivateCandidatesPerProjectTest(t, db)
	})

	t.Run("RunFindDocInfo test", func(t *testing.T) {
		testcases.RunFindDocInfoTest(t, db, projectID)
	})

	t.Run("RunFindDocInfosByKeys test", func(t *testing.T) {
		testcases.RunFindDocInfosByKeysTest(t, db, projectID)
	})

	t.Run("RunFindDocInfosByQuery test", func(t *testing.T) {
		testcases.RunFindDocInfosByQueryTest(t, db, projectOneID)
	})

	t.Run("RunFindChangesBetweenServerSeqs test", func(t *testing.T) {
		testcases.RunFindChangesBetweenServerSeqsTest(t, db, projectID)
	})

	t.Run("RunFindChangeInfosBetweenServerSeqsTest test", func(t *testing.T) {
		testcases.RunFindChangeInfosBetweenServerSeqsTest(t, db, projectID)
	})

	t.Run("RunFindLatestChangeInfoTest test", func(t *testing.T) {
		testcases.RunFindLatestChangeInfoTest(t, db, projectID)
	})

	t.Run("RunFindClosestSnapshotInfo test", func(t *testing.T) {
		testcases.RunFindClosestSnapshotInfoTest(t, db, projectID)
	})

	t.Run("ListUserInfos test", func(t *testing.T) {
		testcases.RunListUserInfosTest(t, db)
	})

	t.Run("FindUserInfoByID test", func(t *testing.T) {
		testcases.RunFindUserInfoByIDTest(t, db)
	})

	t.Run("FindUserInfoByName test", func(t *testing.T) {
		testcases.RunFindUserInfoByNameTest(t, db)
	})

	t.Run("FindProjectInfoBySecretKey test", func(t *testing.T) {
		testcases.RunFindProjectInfoBySecretKeyTest(t, db)
	})

	t.Run("FindProjectInfoByName test", func(t *testing.T) {
		testcases.RunFindProjectInfoByNameTest(t, db)
	})

	t.Run("ActivateClientDeactivateClient test", func(t *testing.T) {
		testcases.RunActivateClientDeactivateClientTest(t, db, projectID)
	})

	t.Run("TryAttachingAndDeactivateClient test", func(t *testing.T) {
		testcases.RunTryAttachingAndDeactivateClientTest(t, db, projectID)
	})

	t.Run("UpdateProjectInfo test", func(t *testing.T) {
		testcases.RunUpdateProjectInfoTest(t, db)
	})

	t.Run("FindDocInfosByPaging test", func(t *testing.T) {
		testcases.RunFindDocInfosByPagingTest(t, db, projectTwoID)
	})

	t.Run("CreateClientInfo test", func(t *testing.T) {
		testcases.RunCreateChangeInfosTest(t, db, projectID)
	})

	t.Run("UpdateClientInfoAfterPushPull test", func(t *testing.T) {
		testcases.RunUpdateClientInfoAfterPushPullTest(t, db, projectID)
	})

	t.Run("IsDocumentAttached test", func(t *testing.T) {
		testcases.RunIsDocumentAttachedTest(t, db, projectID)
	})

	t.Run("FindClientInfosByAttachedDocRefKey test", func(t *testing.T) {
		testcases.RunFindClientInfosByAttachedDocRefKeyTest(t, db, projectID)
	})

	t.Run("RunPurgeDocument test", func(t *testing.T) {
		testcases.RunPurgeDocument(t, db, projectID)
	})
}
