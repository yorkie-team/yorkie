//go:build bench

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package bench

import (
	"context"
	"fmt"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/test/helper"
)

func getDocKey(b *testing.B, i int) key.Key {
	return key.Key(fmt.Sprintf("tests$%s-%d-%d", b.Name(), i, gotime.Now().UnixMilli()))
}

func setUpBackend(
	b *testing.B,
	snapshotInterval int64,
	snapshotThreshold int64,
) *backend.Backend {
	conf := helper.TestConfig()
	conf.Backend.SnapshotInterval = snapshotInterval
	conf.Backend.SnapshotThreshold = snapshotThreshold

	metrics, err := prometheus.NewMetrics()
	assert.NoError(b, err)

	be, err := backend.New(
		conf.Backend,
		conf.Mongo,
		conf.Housekeeping,
		metrics,
	)
	assert.NoError(b, err)

	return be
}

func setUpDefaultProject(b *testing.B, be *backend.Backend) *types.Project {
	projectInfo, err := be.DB.FindProjectInfoByID(context.Background(), database.DefaultProjectID)
	assert.NoError(b, err)
	return projectInfo.ToProject()
}

func setUpClientsAndDocs(
	ctx context.Context,
	n int,
	docKey key.Key,
	b *testing.B,
	be *backend.Backend,
) ([]*database.ClientInfo, types.ID, []*document.Document) {
	var clientInfos []*database.ClientInfo
	var docID types.ID
	var docs []*document.Document
	for i := 0; i < n; i++ {
		clientInfo, err := be.DB.ActivateClient(ctx, database.DefaultProjectID, fmt.Sprintf("client-%d", i))
		assert.NoError(b, err)
		docInfo, err := be.DB.FindDocInfoByKeyAndOwner(ctx, clientInfo.RefKey(), docKey, true)
		assert.NoError(b, err)
		assert.NoError(b, clientInfo.AttachDocument(docInfo.ID, false))
		assert.NoError(b, be.DB.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo))

		bytesID, _ := clientInfo.ID.Bytes()
		actorID, _ := time.ActorIDFromBytes(bytesID)
		doc := document.New(docKey)
		doc.SetActor(actorID)
		assert.NoError(b, doc.Update(func(root *json.Object, _ *presence.Presence) error {
			root.SetNewArray("array")
			return nil
		}))

		clientInfos = append(clientInfos, clientInfo)
		if docID == "" {
			docID = docInfo.ID
		}
		docs = append(docs, doc)
	}
	return clientInfos, docID, docs
}

func createChangePack(
	cnt int,
	doc *document.Document,
	b *testing.B,
) *change.Pack {
	for idx := 0; idx < cnt; idx++ {
		assert.NoError(b, doc.Update(func(root *json.Object, _ *presence.Presence) error {
			root.GetArray("array").AddString("A")
			return nil
		}))
	}
	return doc.CreateChangePack()
}

func benchmarkPushChanges(
	changeCnt int,
	b *testing.B,
	be *backend.Backend,
	project *types.Project,
) {
	for i := 1; i < b.N; i++ {
		b.StopTimer()
		ctx := context.Background()
		docKey := getDocKey(b, i)
		clientInfos, docID, docs := setUpClientsAndDocs(ctx, 1, docKey, b, be)
		pack := createChangePack(changeCnt, docs[0], b)
		docInfo, err := documents.FindDocInfoByRefKey(ctx, be, types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		})
		assert.NoError(b, err)
		b.StartTimer()

		_, err = packs.PushPull(ctx, be, project, clientInfos[0], docInfo, pack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.NoError(b, err)
	}
}

func benchmarkPullChanges(
	changeCnt int,
	b *testing.B,
	be *backend.Backend,
	project *types.Project,
) {
	for i := 1; i < b.N; i++ {
		b.StopTimer()
		ctx := context.Background()
		docKey := getDocKey(b, i)
		clientInfos, docID, docs := setUpClientsAndDocs(ctx, 2, docKey, b, be)
		pusherClientInfo, pullerClientInfo := clientInfos[0], clientInfos[1]
		pusherDoc, pullerDoc := docs[0], docs[1]
		pushPack := createChangePack(changeCnt, pusherDoc, b)
		pullPack := createChangePack(0, pullerDoc, b)

		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}
		docInfo, err := documents.FindDocInfoByRefKey(ctx, be, docRefKey)
		assert.NoError(b, err)
		_, err = packs.PushPull(ctx, be, project, pusherClientInfo, docInfo, pushPack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.NoError(b, err)

		docInfo, err = documents.FindDocInfoByRefKey(ctx, be, docRefKey)
		assert.NoError(b, err)
		b.StartTimer()

		_, err = packs.PushPull(ctx, be, project, pullerClientInfo, docInfo, pullPack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.NoError(b, err)
	}
}

func benchmarkPushSnapshots(
	snapshotCnt int,
	changeCnt int,
	b *testing.B,
	be *backend.Backend,
	project *types.Project,
) {
	for i := 1; i < b.N; i++ {
		b.StopTimer()
		ctx := context.Background()
		docKey := getDocKey(b, i)
		clientInfos, docID, docs := setUpClientsAndDocs(ctx, 1, docKey, b, be)
		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}
		b.StartTimer()

		for j := 0; j < snapshotCnt; j++ {
			b.StopTimer()
			pushPack := createChangePack(changeCnt, docs[0], b)
			docInfo, err := documents.FindDocInfoByRefKey(ctx, be, docRefKey)
			assert.NoError(b, err)
			b.StartTimer()

			pulled, err := packs.PushPull(ctx, be, project, clientInfos[0], docInfo, pushPack, packs.PushPullOptions{
				Mode:   types.SyncModePushPull,
				Status: document.StatusAttached,
			})
			assert.NoError(b, err)

			b.StopTimer()
			pbChangePack, err := pulled.ToPBChangePack()
			assert.NoError(b, err)
			pullPack, err := converter.FromChangePack(pbChangePack)
			assert.NoError(b, err)
			assert.NoError(b, docs[0].ApplyChangePack(pullPack))
			b.StartTimer()
		}
	}
}

func benchmarkPullSnapshot(
	changeCnt int,
	b *testing.B,
	be *backend.Backend,
	project *types.Project,
) {
	for i := 1; i < b.N; i++ {
		b.StopTimer()
		ctx := context.Background()
		docKey := getDocKey(b, i)
		clientInfos, docID, docs := setUpClientsAndDocs(ctx, 2, docKey, b, be)
		pusherClientInfo, pullerClientInfo := clientInfos[0], clientInfos[1]
		pusherDoc, pullerDoc := docs[0], docs[1]
		pushPack := createChangePack(changeCnt, pusherDoc, b)
		pullPack := createChangePack(0, pullerDoc, b)

		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}
		docInfo, err := documents.FindDocInfoByRefKey(ctx, be, docRefKey)
		assert.NoError(b, err)
		_, err = packs.PushPull(ctx, be, project, pusherClientInfo, docInfo, pushPack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.NoError(b, err)

		docInfo, err = documents.FindDocInfoByRefKey(ctx, be, docRefKey)
		assert.NoError(b, err)
		b.StartTimer()

		_, err = packs.PushPull(ctx, be, project, pullerClientInfo, docInfo, pullPack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.NoError(b, err)
	}
}

func BenchmarkChange(b *testing.B) {
	// Disable to take a snapshot by making the interval and the threshold large.
	be := setUpBackend(b, 100000, 100000)
	project := setUpDefaultProject(b, be)
	b.ResetTimer()

	b.Run("Push 10 Changes", func(b *testing.B) {
		benchmarkPushChanges(10, b, be, project)
	})

	b.Run("Push 100 Changes", func(b *testing.B) {
		benchmarkPushChanges(100, b, be, project)
	})

	b.Run("Push 1000 Changes", func(b *testing.B) {
		benchmarkPushChanges(1000, b, be, project)
	})

	b.Run("Pull 10 Changes", func(b *testing.B) {
		benchmarkPullChanges(10, b, be, project)
	})

	b.Run("Pull 100 Changes", func(b *testing.B) {
		benchmarkPullChanges(100, b, be, project)
	})

	b.Run("Pull 1000 Changes", func(b *testing.B) {
		benchmarkPullChanges(1000, b, be, project)
	})
}

func BenchmarkSnapshot(b *testing.B) {
	be := setUpBackend(b, 10, 10)
	project := setUpDefaultProject(b, be)
	b.ResetTimer()

	b.Run("Push 3KB snapshot", func(b *testing.B) {
		benchmarkPushSnapshots(1, 100, b, be, project)
	})

	b.Run("Push 30KB snapshot", func(b *testing.B) {
		benchmarkPushSnapshots(1, 1000, b, be, project)
	})

	b.Run("Pull 3KB snapshot", func(b *testing.B) {
		benchmarkPullSnapshot(100, b, be, project)
	})

	b.Run("Pull 30KB snapshot", func(b *testing.B) {
		benchmarkPullSnapshot(1000, b, be, project)
	})
}
