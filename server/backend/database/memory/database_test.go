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
	"github.com/yorkie-team/yorkie/pkg/key"
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

	for i := 0; i < 5; i++ {
		_, err := db.CreateProjectInfo(ctx, fmt.Sprintf("p%d", i), testOwnerID)
		assert.NoError(t, err)
	}

	// Page 1: first three projects starting from ZeroID.
	page1, page1LastID, err := db.FindProjectInfosForRefresh(ctx, 3, database.ZeroID)
	assert.NoError(t, err)
	assert.Len(t, page1, 3)

	// Page 2: remaining two projects starting from page1LastID.
	page2, page2LastID, err := db.FindProjectInfosForRefresh(ctx, 3, page1LastID)
	assert.NoError(t, err)
	assert.Len(t, page2, 2)

	// Distinctness: no ID overlap between page 1 and page 2.
	seen := make(map[types.ID]struct{}, len(page1)+len(page2))
	for _, info := range page1 {
		seen[info.ID] = struct{}{}
	}
	for _, info := range page2 {
		_, dup := seen[info.ID]
		assert.False(t, dup, "page2 ID %s overlaps with page1", info.ID)
		seen[info.ID] = struct{}{}
	}

	// Ordering: concatenated IDs are strictly ascending.
	all := make([]*database.ProjectInfo, 0, len(page1)+len(page2))
	all = append(all, page1...)
	all = append(all, page2...)
	for i := 1; i < len(all); i++ {
		assert.True(t, all[i-1].ID < all[i].ID,
			"expected strictly ascending order: %s < %s", all[i-1].ID, all[i].ID)
	}

	// Page 3 (wrap): iteration exhausted, returns no infos and ZeroID.
	page3, page3LastID, err := db.FindProjectInfosForRefresh(ctx, 3, page2LastID)
	assert.NoError(t, err)
	assert.Len(t, page3, 0)
	assert.Equal(t, database.ZeroID, page3LastID)
}

func TestFindProjectInfosForRefreshIncludesDefaultProject(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)

	// The default project has ID == ZeroID. Without inclusive-boundary handling
	// on the first cycle, it would be silently skipped forever by every cursor
	// walk starting at ZeroID.
	_, defaultProject, err := db.EnsureDefaultUserAndProject(ctx, "admin", "admin")
	assert.NoError(t, err)
	assert.Equal(t, database.ZeroID, defaultProject.ID)

	other, err := db.CreateProjectInfo(ctx, "other", testOwnerID)
	assert.NoError(t, err)

	// First cycle: ZeroID cursor must include the default project.
	page1, page1LastID, err := db.FindProjectInfosForRefresh(ctx, 10, database.ZeroID)
	assert.NoError(t, err)
	assert.Len(t, page1, 2)
	assert.Equal(t, defaultProject.ID, page1[0].ID)
	assert.Equal(t, other.ID, page1[1].ID)
	assert.Equal(t, other.ID, page1LastID)

	// Subsequent cycle: cursor advanced past `other`, iteration exhausted.
	page2, page2LastID, err := db.FindProjectInfosForRefresh(ctx, 10, page1LastID)
	assert.NoError(t, err)
	assert.Len(t, page2, 0)
	assert.Equal(t, database.ZeroID, page2LastID)

	// Next term begins by restarting with ZeroID; default is included again.
	page3, _, err := db.FindProjectInfosForRefresh(ctx, 10, database.ZeroID)
	assert.NoError(t, err)
	assert.Len(t, page3, 2)
	assert.Equal(t, defaultProject.ID, page3[0].ID)
}

func TestCountActivatedClients(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)

	project, err := db.CreateProjectInfo(ctx, t.Name(), testOwnerID)
	assert.NoError(t, err)

	// Activate 3 clients.
	var clients []*database.ClientInfo
	for i := 0; i < 3; i++ {
		info, err := db.ActivateClient(ctx, project.ID, fmt.Sprintf("%s-c%d", t.Name(), i), nil)
		assert.NoError(t, err)
		clients = append(clients, info)
	}

	count, err := db.CountActivatedClients(ctx, project.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), count)

	// Deactivate one client.
	_, err = db.DeactivateClient(ctx, clients[0].RefKey())
	assert.NoError(t, err)

	count, err = db.CountActivatedClients(ctx, project.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestCountAliveDocuments(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)

	project, err := db.CreateProjectInfo(ctx, t.Name(), testOwnerID)
	assert.NoError(t, err)

	clientInfo, err := db.ActivateClient(ctx, project.ID, t.Name(), nil)
	assert.NoError(t, err)

	// Create 2 documents.
	var docs []*database.DocInfo
	for i := 0; i < 2; i++ {
		docKey := key.Key(fmt.Sprintf("tests$%s-d%d", t.Name(), i))
		docInfo, err := db.FindOrCreateDocInfo(ctx, clientInfo.RefKey(), docKey, false)
		assert.NoError(t, err)
		docs = append(docs, docInfo)
	}

	count, err := db.CountAliveDocuments(ctx, project.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Mark one document as removed.
	assert.NoError(t, db.UpdateDocInfoStatusToRemoved(ctx, docs[0].RefKey()))

	count, err = db.CountAliveDocuments(ctx, project.ID)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
