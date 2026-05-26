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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/database/testcases"
)

const (
	projectID    = types.ID("000000000000000000000000")
	projectOneID = types.ID("000000000000000000000001")
	projectTwoID = types.ID("000000000000000000000002")
	testOwnerID  = types.ID("000000000000000000000000")
)

func TestDB(t *testing.T) {
	db, err := memory.New()
	assert.NoError(t, err)

	t.Run("RunLeadership Test", func(t *testing.T) {
		testcases.RunLeadershipTest(t, db)
	})

	t.Run("RunFindDocInfo test", func(t *testing.T) {
		testcases.RunFindDocInfoTest(t, db, projectID)
	})

	t.Run("RunFindDocInfosByKeysAndIDs test", func(t *testing.T) {
		testcases.RunFindDocInfosByKeysAndIDsTest(t, db, projectID)
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

	t.Run("ListMemberInfos test", func(t *testing.T) {
		testcases.RunListMemberInfosTest(t, db)
	})

	t.Run("UpdateMemberRole test", func(t *testing.T) {
		testcases.RunUpdateMemberRoleTest(t, db)
	})

	t.Run("DeleteMemberInfo test", func(t *testing.T) {
		testcases.RunDeleteMemberInfoTest(t, db)
	})

	t.Run("CreateInviteInfo test", func(t *testing.T) {
		testcases.RunCreateInviteInfoTest(t, db)
	})

	t.Run("FindInviteInfoByToken test", func(t *testing.T) {
		testcases.RunFindInviteInfoByTokenTest(t, db)
	})

	t.Run("DeleteExpiredInviteInfos test", func(t *testing.T) {
		testcases.RunDeleteExpiredInviteInfosTest(t, db)
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

	t.Run("IsDocumentAttachedOrAttaching test", func(t *testing.T) {
		testcases.RunIsDocumentAttachedOrAttachingTest(t, db, projectID)
	})

	t.Run("FindClientInfosByAttachedDocRefKey test", func(t *testing.T) {
		testcases.RunFindClientInfosByAttachedDocRefKeyTest(t, db, projectID)
	})

	t.Run("FindAttachedClientCountsByDocIDs test", func(t *testing.T) {
		testcases.RunFindAttachedClientCountsByDocIDsTest(t, db, projectID)
	})

	t.Run("RunPurgeDocument test", func(t *testing.T) {
		testcases.RunPurgeDocument(t, db, projectID)
	})

	t.Run("RunFindCandidates test", func(t *testing.T) {
		testcases.RunFindCandidatesTest(t, db, projectID)
	})

	t.Run("FindCompactionCandidates test", func(t *testing.T) {
		testcases.RunFindCompactionCandidatesTest(t, db, projectID)
	})
}

func TestProjectStatsCounts(t *testing.T) {
	ctx := context.Background()

	t.Run("returns zeros when project has no stats fields", func(t *testing.T) {
		db, err := memory.New()
		assert.NoError(t, err)

		info, err := db.CreateProjectInfo(ctx, t.Name(), testOwnerID)
		assert.NoError(t, err)

		counts, err := db.GetProjectStatsCounts(ctx, info.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), counts.ClientsCount)
		assert.Equal(t, int64(0), counts.DocumentsCount)
		assert.True(t, counts.UpdatedAt.IsZero())
	})

	t.Run("returns updated values after UpdateProjectStats", func(t *testing.T) {
		db, err := memory.New()
		assert.NoError(t, err)

		info, err := db.CreateProjectInfo(ctx, t.Name(), testOwnerID)
		assert.NoError(t, err)

		now := time.Now()
		assert.NoError(t, db.UpdateProjectStats(ctx, info.ID, 100, 50, now))

		counts, err := db.GetProjectStatsCounts(ctx, info.ID)
		assert.NoError(t, err)
		assert.Equal(t, int64(100), counts.ClientsCount)
		assert.Equal(t, int64(50), counts.DocumentsCount)
		assert.WithinDuration(t, now, counts.UpdatedAt, time.Millisecond)
	})
}

func TestFindProjectInfosForRefresh(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)

	var ids []types.ID
	for i := 0; i < 5; i++ {
		info, err := db.CreateProjectInfo(ctx, fmt.Sprintf("%s-p%d", t.Name(), i), testOwnerID)
		assert.NoError(t, err)
		ids = append(ids, info.ID)
	}

	page1, lastID, err := db.FindProjectInfosForRefresh(ctx, 3, database.ZeroID)
	assert.NoError(t, err)
	assert.Len(t, page1, 3)

	page2, _, err := db.FindProjectInfosForRefresh(ctx, 3, lastID)
	assert.NoError(t, err)
	assert.Len(t, page2, 2)
}
