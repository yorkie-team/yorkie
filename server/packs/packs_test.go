/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	// ErrUpdateClientInfoFailed occurs when updating ClientInfo failed
	// for testing purposes.
	ErrUpdateClientInfoFailed = errors.New("updating clientinfo failed")
)

var (
	testRPCServer *rpc.Server
	testRPCAddr   = fmt.Sprintf("localhost:%d", helper.RPCPort)
	testClient    v1connect.YorkieServiceClient
	testBackend   *backend.Backend
	testMockDB    *MockDB
)

// MockDB represents a mock database for testing purposes
type MockDB struct {
	database.Database
	mockUpdateClientInfoAfterPushPull func(context.Context, *database.ClientInfo, *database.DocInfo) error
}

// NewMockDB returns a mock database with a real database
func NewMockDB(database database.Database) *MockDB {
	return &MockDB{
		Database: database,
	}
}

func (m *MockDB) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	if m.mockUpdateClientInfoAfterPushPull != nil {
		return m.mockUpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
	}
	return m.Database.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
}

func TestMain(m *testing.M) {
	met, err := prometheus.NewMetrics()
	if err != nil {
		log.Fatal(err)
	}

	testBackend, err = backend.New(
		&backend.Config{
			AdminUser:                   helper.AdminUser,
			AdminPassword:               helper.AdminPassword,
			UseDefaultProject:           helper.UseDefaultProject,
			ClientDeactivateThreshold:   helper.ClientDeactivateThreshold,
			SnapshotThreshold:           helper.SnapshotThreshold,
			AuthWebhookCacheSize:        helper.AuthWebhookSize,
			AuthWebhookCacheTTL:         helper.AuthWebhookCacheTTL.String(),
			AuthWebhookMaxWaitInterval:  helper.AuthWebhookMaxWaitInterval.String(),
			AuthWebhookMinWaitInterval:  helper.AuthWebhookMinWaitInterval.String(),
			AuthWebhookRequestTimeout:   helper.AuthWebhookRequestTimeout.String(),
			EventWebhookMaxWaitInterval: helper.EventWebhookMaxWaitInterval.String(),
			EventWebhookMinWaitInterval: helper.EventWebhookMinWaitInterval.String(),
			EventWebhookRequestTimeout:  helper.EventWebhookRequestTimeout.String(),
			ProjectCacheSize:            helper.ProjectCacheSize,
			ProjectCacheTTL:             helper.ProjectCacheTTL.String(),
			AdminTokenDuration:          helper.AdminTokenDuration,
		}, &mongo.Config{
			ConnectionURI:     helper.MongoConnectionURI,
			YorkieDatabase:    helper.TestDBName(),
			ConnectionTimeout: helper.MongoConnectionTimeout,
			PingTimeout:       helper.MongoPingTimeout,
		}, &housekeeping.Config{
			Interval:                  helper.HousekeepingInterval.String(),
			CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
			ProjectFetchSize:          helper.HousekeepingProjectFetchSize,
		}, met, nil)
	if err != nil {
		log.Fatal(err)
	}
	testMockDB = NewMockDB(testBackend.DB)
	testBackend.DB = testMockDB

	project, err := testBackend.DB.FindProjectInfoByID(
		context.Background(),
		database.DefaultProjectID,
	)
	if err != nil {
		log.Fatal(err)
	}

	testRPCServer, err = rpc.NewServer(
		&rpc.Config{
			Port: helper.RPCPort,
		}, testBackend,
	)
	if err != nil {
		log.Fatal(err)
	}

	if err = testRPCServer.Start(); err != nil {
		log.Fatalf("failed rpc listen: %s\n", err)
	}
	if err = helper.WaitForServerToStart(testRPCAddr); err != nil {
		log.Fatal(err)
	}

	authInterceptor := client.NewAuthInterceptor(project.PublicKey, "")

	conn := http.DefaultClient
	testClient = v1connect.NewYorkieServiceClient(
		conn,
		"http://"+testRPCAddr,
		connect.WithInterceptors(authInterceptor),
	)

	code := m.Run()

	if err := testBackend.Shutdown(); err != nil {
		log.Fatal(err)
	}
	testRPCServer.Shutdown(true)
	os.Exit(code)
}

func triggerErrUpdateClientInfo(on bool) {
	if on {
		testMockDB.mockUpdateClientInfoAfterPushPull = func(
			context.Context,
			*database.ClientInfo,
			*database.DocInfo,
		) error {
			return ErrUpdateClientInfoFailed
		}
	} else {
		testMockDB.mockUpdateClientInfoAfterPushPull = nil
	}
}

func TestPacks(t *testing.T) {
	t.Run("cannot detect change duplication due to clientInfo update failure", func(t *testing.T) {
		t.Skip("remove this after resolving pushpull consistency problem")
		ctx := context.Background()

		projectInfo, err := testBackend.DB.FindProjectInfoByID(
			ctx,
			database.DefaultProjectID,
		)
		assert.NoError(t, err)
		project := projectInfo.ToProject()

		triggerErrUpdateClientInfo(false)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: helper.TestDocKey(t).String()}))
		assert.NoError(t, err)

		clientID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t).String(),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
					Changes: []*api.Change{
						{
							Id: &api.ChangeID{
								ClientSeq: 1,
								Lamport:   1,
								ActorId:   clientID,
							},
						},
					},
				},
			},
			))
		assert.NoError(t, err)

		actorID, err := time.ActorIDFromBytes(clientID)
		assert.NoError(t, err)

		docID := types.ID(resPack.Msg.DocumentId)
		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}

		// 0. Check docInfo.ServerSeq and clientInfo.Checkpoint
		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), docInfo.ServerSeq)

		clientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		assert.Equal(t, int64(1), clientInfo.Checkpoint(docID).ServerSeq)
		assert.Equal(t, uint32(1), clientInfo.Checkpoint(docID).ClientSeq)

		// 1. Create a ChangePack with a single Change
		pack, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 2},
			Changes: []*api.Change{
				{
					Id: &api.ChangeID{
						ClientSeq: 2,
						Lamport:   2,
						ActorId:   clientID,
					},
				},
			},
		})
		assert.NoError(t, err)

		// 2-1. An arbitrary failure occurs while updating clientInfo
		triggerErrUpdateClientInfo(true)

		_, err = packs.PushPull(ctx, testBackend, project, clientInfo, docInfo, pack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.ErrorIs(t, err, ErrUpdateClientInfoFailed)

		triggerErrUpdateClientInfo(false)

		// 2-2. pushed change is stored in the database
		changes, err := packs.FindChanges(ctx, testBackend, docInfo, 2, 2)
		assert.NoError(t, err)
		assert.Len(t, changes, 1)

		// 2-3. docInfo.ServerSeq increases from 1 to 2
		docInfo, err = documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), docInfo.ServerSeq)

		// 2-4. clientInfo.Checkpoint has not been updated
		clientInfo, err = clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		assert.Equal(t, int64(1), clientInfo.Checkpoint(docID).ServerSeq)
		assert.Equal(t, uint32(1), clientInfo.Checkpoint(docID).ClientSeq)

		// 3-1. A duplicate request is sent
		_, err = packs.PushPull(ctx, testBackend, project, clientInfo, docInfo, pack, packs.PushPullOptions{
			Mode:   types.SyncModePushPull,
			Status: document.StatusAttached,
		})
		assert.NoError(t, err)

		// 3-2. duplicated change is not stored in the database
		changes, err = packs.FindChanges(ctx, testBackend, docInfo, 3, 3)
		assert.NoError(t, err)
		assert.Len(t, changes, 0)

		// 3-3. The server should detect the duplication and not update docInfo.ServerSeq
		docInfo, err = documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), docInfo.ServerSeq)

		// 3-4. clientInfo.Checkpoint has been updated properly
		clientInfo, err = clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		assert.Equal(t, int64(2), clientInfo.Checkpoint(docID).ServerSeq)
		assert.Equal(t, uint32(2), clientInfo.Checkpoint(docID).ClientSeq)
	})
}
