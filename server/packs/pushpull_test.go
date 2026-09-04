//go:build integration

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
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
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
	testRPCAddr   = fmt.Sprintf("localhost:%d", helper.PacksRPCPort)
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
		helper.TestBackendConfig(),
		helper.TestMongoConfig(),
		helper.TestMembershipConfig(),
		helper.TestHousekeepingConfig(),
		met, nil, nil,
	)
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
			Port:              helper.PacksRPCPort,
			ReadHeaderTimeout: helper.RPCReadHeaderTimeout,
			IdleTimeout:       helper.RPCIdleTimeout,
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
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: helper.TestKey(t).String()}))
		assert.NoError(t, err)

		clientID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestKey(t).String(),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
					Changes:     []*api.Change{{Id: &api.ChangeID{ClientSeq: 1, Lamport: 1, ActorId: clientID}}},
				},
			}),
		)
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
			DocumentKey: helper.TestKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 2},
			Changes: []*api.Change{
				{Id: &api.ChangeID{ClientSeq: 2, Lamport: 2, ActorId: clientID}},
			},
		})
		assert.NoError(t, err)

		// 2-1. An arbitrary failure occurs while updating clientInfo
		triggerErrUpdateClientInfo(true)

		_, err = packs.PushPull(ctx, testBackend, project, clientInfo, docInfo.RefKey(), pack, packs.PushPullOptions{
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
		_, err = packs.PushPull(ctx, testBackend, project, clientInfo, docInfo.RefKey(), pack, packs.PushPullOptions{
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

	t.Run("non-sequential client seq is rejected", func(t *testing.T) {
		ctx := context.Background()

		projectInfo, err := testBackend.DB.FindProjectInfoByID(
			ctx,
			database.DefaultProjectID,
		)
		assert.NoError(t, err)
		project := projectInfo.ToProject()

		activateResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{
				ClientKey: helper.TestKey(t).String(),
			}),
		)
		assert.NoError(t, err)

		clientID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestKey(t).String(),
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
			}),
		)
		assert.NoError(t, err)

		actorID, err := time.ActorIDFromBytes(clientID)
		assert.NoError(t, err)

		docID := types.ID(resPack.Msg.DocumentId)
		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}

		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)

		clientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		clientRefKey := types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		}
		docServerSeqBefore := docInfo.ServerSeq
		clientCPBefore := clientInfo.Checkpoint(docID)

		pack, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: helper.TestKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 2},
			Changes: []*api.Change{
				{
					Id: &api.ChangeID{
						ClientSeq: 3,
						Lamport:   2,
						ActorId:   clientID,
					},
				},
			},
		})
		assert.NoError(t, err)

		_, err = packs.PushPull(
			ctx,
			testBackend,
			project,
			clientInfo,
			docInfo.RefKey(),
			pack,
			packs.PushPullOptions{
				Mode:   types.SyncModePushPull,
				Status: document.StatusAttached,
			},
		)

		assert.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assertRejectedPushPullUnchanged(t, ctx, docRefKey, clientRefKey, docID, docServerSeqBefore, clientCPBefore)
	})

	t.Run("future server seq checkpoint is rejected", func(t *testing.T) {
		ctx := context.Background()

		projectInfo, err := testBackend.DB.FindProjectInfoByID(
			ctx,
			database.DefaultProjectID,
		)
		assert.NoError(t, err)
		project := projectInfo.ToProject()

		activateResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{
				ClientKey: helper.TestKey(t).String(),
			}),
		)
		assert.NoError(t, err)

		clientID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestKey(t).String(),
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
			}),
		)
		assert.NoError(t, err)

		actorID, err := time.ActorIDFromBytes(clientID)
		assert.NoError(t, err)

		docID := types.ID(resPack.Msg.DocumentId)
		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}

		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)

		clientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		clientRefKey := types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		}
		docServerSeqBefore := docInfo.ServerSeq
		clientCPBefore := clientInfo.Checkpoint(docID)

		pack, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: helper.TestKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 2, ClientSeq: 2},
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

		_, err = packs.PushPull(
			ctx,
			testBackend,
			project,
			clientInfo,
			docInfo.RefKey(),
			pack,
			packs.PushPullOptions{
				Mode:   types.SyncModePushPull,
				Status: document.StatusAttached,
			},
		)

		assert.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assertRejectedPushPullUnchanged(t, ctx, docRefKey, clientRefKey, docID, docServerSeqBefore, clientCPBefore)
	})

	t.Run("client seq gaps remain rejected with disable presence", func(t *testing.T) {
		ctx := context.Background()

		projectInfo, err := testBackend.DB.FindProjectInfoByID(
			ctx,
			database.DefaultProjectID,
		)
		assert.NoError(t, err)
		project := projectInfo.ToProject()

		activateResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{
				ClientKey: helper.TestKey(t).String(),
			}),
		)
		assert.NoError(t, err)

		clientID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestKey(t).String(),
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
			}),
		)
		assert.NoError(t, err)

		actorID, err := time.ActorIDFromBytes(clientID)
		assert.NoError(t, err)

		docID := types.ID(resPack.Msg.DocumentId)
		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}

		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)

		clientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		clientRefKey := types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		}
		docServerSeqBefore := docInfo.ServerSeq
		clientCPBefore := clientInfo.Checkpoint(docID)

		// Gap: after attach (ClientSeq=1), presence-only occupies 2 and the
		// document change jumps to 4. Stripping presence first would leave
		// only ClientSeq=4 and hide the hole; validation must see the raw pack.
		pack, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: helper.TestKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 4},
			Changes: []*api.Change{
				{
					Id: &api.ChangeID{
						ClientSeq: 2,
						Lamport:   2,
						ActorId:   clientID,
					},
					PresenceChange: &api.PresenceChange{
						Type: api.PresenceChange_CHANGE_TYPE_PUT,
						Presence: &api.Presence{
							Data: map[string]string{"k": "v"},
						},
					},
				},
				{
					Id: &api.ChangeID{
						ClientSeq: 4,
						Lamport:   3,
						ActorId:   clientID,
					},
				},
			},
		})
		assert.NoError(t, err)

		_, err = packs.PushPull(
			ctx,
			testBackend,
			project,
			clientInfo,
			docInfo.RefKey(),
			pack,
			packs.PushPullOptions{
				Mode:            types.SyncModePushPull,
				Status:          document.StatusAttached,
				DisablePresence: true,
			},
		)

		assert.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assertRejectedPushPullUnchanged(t, ctx, docRefKey, clientRefKey, docID, docServerSeqBefore, clientCPBefore)
	})

	t.Run("disable presence keeps continuous interleaved client seq", func(t *testing.T) {
		ctx := context.Background()

		projectInfo, err := testBackend.DB.FindProjectInfoByID(
			ctx,
			database.DefaultProjectID,
		)
		assert.NoError(t, err)
		project := projectInfo.ToProject()

		activateResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{
				ClientKey: helper.TestKey(t).String(),
			}),
		)
		assert.NoError(t, err)

		clientID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestKey(t).String(),
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
			}),
		)
		assert.NoError(t, err)

		actorID, err := time.ActorIDFromBytes(clientID)
		assert.NoError(t, err)

		docID := types.ID(resPack.Msg.DocumentId)
		docRefKey := types.DocRefKey{
			ProjectID: project.ID,
			DocID:     docID,
		}

		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)

		clientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)

		// Continuous: presence-only ClientSeq=2 then document ClientSeq=3.
		// Strip drops the presence change, but validation must accept the
		// original continuous sequence.
		pack, err := converter.FromChangePack(&api.ChangePack{
			DocumentKey: helper.TestKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 3},
			Changes: []*api.Change{
				{
					Id: &api.ChangeID{
						ClientSeq: 2,
						Lamport:   2,
						ActorId:   clientID,
					},
					PresenceChange: &api.PresenceChange{
						Type: api.PresenceChange_CHANGE_TYPE_PUT,
						Presence: &api.Presence{
							Data: map[string]string{"k": "v"},
						},
					},
				},
				{
					Id: &api.ChangeID{
						ClientSeq: 3,
						Lamport:   3,
						ActorId:   clientID,
					},
				},
			},
		})
		assert.NoError(t, err)

		_, err = packs.PushPull(
			ctx,
			testBackend,
			project,
			clientInfo,
			docInfo.RefKey(),
			pack,
			packs.PushPullOptions{
				Mode:            types.SyncModePushPull,
				Status:          document.StatusAttached,
				DisablePresence: true,
			},
		)
		assert.NoError(t, err)

		clientInfoAfter, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(actorID),
		})
		assert.NoError(t, err)
		assert.Equal(t, uint32(3), clientInfoAfter.Checkpoint(docID).ClientSeq)
	})

	// Regression guard for the offline "resume" attach path: the acked-baseline
	// seeding in AttachDocument (server/rpc/yorkie_server.go). On resume the SDK
	// presents a PROJECTED checkpoint (createChangePack calls
	// increaseClientSeq(len(changes))), so ClientSeq = acked baseline + number
	// of pending changes. The RPC recovers the baseline via
	// pack.Checkpoint.ClientSeq - len(pack.Changes) before seeding ClientDocInfo
	// (Case B). If that recovery regresses to seeding the projected value, the
	// resumed client's pending changes have clientSeq <= the seeded checkpoint
	// and pushpull silently drops them as already-pushed — the un-pushed edits
	// vanish. This drives the same RPC attach path a restored SDK client uses
	// and asserts the pending change is accepted, not dropped.
	t.Run("resumed attach seeds the acked baseline and keeps pending changes", func(t *testing.T) {
		ctx := context.Background()

		projectInfo, err := testBackend.DB.FindProjectInfoByID(ctx, database.DefaultProjectID)
		assert.NoError(t, err)
		project := projectInfo.ToProject()

		clientKey := helper.TestKey(t).String()
		docKey := helper.TestKey(t).String()

		// 01. Initial session: activate, attach with one local change, so the
		// doc's server_seq advances to 1 and the client's checkpoint is acked at
		// (serverSeq=1, clientSeq=1). The change is stamped with the stable actor
		// (ActivateClientResponse.ActorId), exactly as a real SDK client does via
		// doc.setActor; this actor is deterministic per (project, clientKey), so
		// the resumed session below reuses it.
		activateResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: clientKey}),
		)
		assert.NoError(t, err)
		stableActorHex := activateResp.Msg.ActorId
		stableActor, err := hex.DecodeString(stableActorHex)
		assert.NoError(t, err)

		resPack, err := testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: docKey,
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
					Changes: []*api.Change{{
						Id: &api.ChangeID{ClientSeq: 1, Lamport: 1, ActorId: stableActor},
					}},
				},
			}),
		)
		assert.NoError(t, err)

		docID := types.ID(resPack.Msg.DocumentId)
		docRefKey := types.DocRefKey{ProjectID: project.ID, DocID: docID}

		docInfo, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)
		// The acked baseline the client persists locally: serverSeq=1, clientSeq=1.
		assert.Equal(t, int64(1), docInfo.ServerSeq)

		// Detach models the tab closing; the reload runs a fresh ActivateClient
		// with no server memory of the prior checkpoint (Case B).
		_, err = testClient.DetachDocument(
			ctx,
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: &api.ChangePack{
					DocumentKey: docKey,
					Checkpoint:  &api.Checkpoint{ServerSeq: 1, ClientSeq: 2},
					Changes: []*api.Change{{
						Id: &api.ChangeID{ClientSeq: 2, Lamport: 2, ActorId: stableActor},
					}},
				},
			}),
		)
		assert.NoError(t, err)

		docInfo, err = documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)
		serverSeqAfterDetach := docInfo.ServerSeq

		// 02. Resume: a fresh ActivateClient with the same clientKey yields a new
		// per-session client row (Case B: no ClientDocInfo history) but the same
		// stable actor. The restored SDK client presents its PROJECTED checkpoint:
		// acked baseline clientSeq=1 + one pending change = clientSeq=2, carrying
		// that pending change at clientSeq=2. ServerSeq=1 is the baseline it saw.
		resumeResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: clientKey}),
		)
		assert.NoError(t, err)
		// The stable actor must survive reactivation; otherwise the pending
		// change's lineage would not be recognized as this client's own.
		assert.Equal(t, stableActorHex, resumeResp.Msg.ActorId)
		// A genuine reload is a new session row, not the old one.
		assert.NotEqual(t, activateResp.Msg.ClientId, resumeResp.Msg.ClientId)

		// The pending change carries a presence PUT so its effect is durably
		// stored and pulled by an observer. (A change with no operations and no
		// presence advances server_seq but leaves no retrievable row in the
		// Mongo backend, so presence gives the resume an observable footprint.)
		_, err = testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: resumeResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: docKey,
					// PROJECTED checkpoint: ClientSeq = baseline(1) + pending(1).
					Checkpoint: &api.Checkpoint{ServerSeq: 1, ClientSeq: 2},
					Changes: []*api.Change{{
						Id: &api.ChangeID{ClientSeq: 2, Lamport: 3, ActorId: stableActor},
						PresenceChange: &api.PresenceChange{
							Type: api.PresenceChange_CHANGE_TYPE_PUT,
							Presence: &api.Presence{
								Data: map[string]string{"resumed": "true"},
							},
						},
					}},
				},
			}),
		)
		assert.NoError(t, err)

		// 03. Assert the pending change was ACCEPTED and applied, not dropped.
		// If the baseline recovery regressed (seed the projected clientSeq=2),
		// pushpull would drop the pending change at clientSeq=2 as already-pushed
		// and server_seq would stay at serverSeqAfterDetach.
		docInfo, err = documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
		assert.NoError(t, err)
		assert.Equal(t, serverSeqAfterDetach+1, docInfo.ServerSeq,
			"resumed pending change must advance server_seq, not be dropped as already-pushed")

		// The resumed session's checkpoint must reflect the pushed pending change.
		resumeActorID, err := time.ActorIDFromHex(resumeResp.Msg.ClientId)
		assert.NoError(t, err)
		resumeClientInfo, err := clients.FindActiveClientInfo(ctx, testBackend, types.ClientRefKey{
			ProjectID: project.ID,
			ClientID:  types.IDFromActorID(resumeActorID),
		})
		assert.NoError(t, err)
		assert.Equal(t, uint32(2), resumeClientInfo.Checkpoint(docID).ClientSeq)
		assert.Equal(t, docInfo.ServerSeq, resumeClientInfo.Checkpoint(docID).ServerSeq)

		// The resumed change is durably stored at the new server_seq (the
		// empty-op changes at earlier seqs leave no retrievable row, but the
		// presence-carrying resumed change does).
		stored, err := packs.FindChanges(ctx, testBackend, docInfo, docInfo.ServerSeq, docInfo.ServerSeq)
		assert.NoError(t, err)
		assert.Len(t, stored, 1)
		assert.Equal(t, uint32(2), stored[0].ID().ClientSeq())

		// 04. A fresh observer client attaches and pulls; it must see the resumed
		// change's effect. Its checkpoint advances to the current server_seq and
		// the resumed presence change is among the pulled changes. If the resume
		// had been dropped, server_seq would sit one short and the presence
		// change would be absent.
		observerResp, err := testClient.ActivateClient(
			ctx,
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: helper.TestKey(t).String() + "-observer"}),
		)
		assert.NoError(t, err)

		observerPack, err := testClient.AttachDocument(
			ctx,
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: observerResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: docKey,
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
				},
			}),
		)
		assert.NoError(t, err)
		assert.Equal(t, docInfo.ServerSeq, observerPack.Msg.ChangePack.Checkpoint.ServerSeq)

		var sawResumedPresence bool
		for _, c := range observerPack.Msg.ChangePack.Changes {
			if pc := c.GetPresenceChange(); pc != nil && pc.GetPresence().GetData()["resumed"] == "true" {
				sawResumedPresence = true
			}
		}
		assert.True(t, sawResumedPresence, "observer must pull the resumed change's presence effect")
	})
}

// assertRejectedPushPullUnchanged reloads DocInfo/ClientInfo after a rejected
// PushPull and asserts neither document nor client checkpoints advanced.
func assertRejectedPushPullUnchanged(
	t *testing.T,
	ctx context.Context,
	docRefKey types.DocRefKey,
	clientRefKey types.ClientRefKey,
	docID types.ID,
	docServerSeqBefore int64,
	clientCPBefore change.Checkpoint,
) {
	t.Helper()

	docInfoAfter, err := documents.FindDocInfoByRefKey(ctx, testBackend, docRefKey)
	assert.NoError(t, err)
	assert.Equal(t, docServerSeqBefore, docInfoAfter.ServerSeq)

	clientInfoAfter, err := clients.FindActiveClientInfo(ctx, testBackend, clientRefKey)
	assert.NoError(t, err)
	assert.Equal(t, clientCPBefore.ServerSeq, clientInfoAfter.Checkpoint(docID).ServerSeq)
	assert.Equal(t, clientCPBefore.ClientSeq, clientInfoAfter.Checkpoint(docID).ClientSeq)
}
