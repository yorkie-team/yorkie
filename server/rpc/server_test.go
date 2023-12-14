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

package rpc_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yorkie-team/yorkie/admin"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	defaultProjectName = "default"
	invalidSlugName    = "@#$%^&*()_+"

	nilClientID     = "000000000000000000000000"
	emptyClientID   = ""
	invalidClientID = "invalid"

	testRPCServer            *rpc.Server
	testRPCAddr              = fmt.Sprintf("localhost:%d", helper.RPCPort)
	testClient               v1connect.YorkieServiceClient
	testAdminAuthInterceptor *admin.AuthInterceptor
	testAdminClient          v1connect.AdminServiceClient

	invalidChangePack = &api.ChangePack{
		DocumentKey: "invalid",
		Checkpoint:  nil,
	}
)

func TestMain(m *testing.M) {
	met, err := prometheus.NewMetrics()
	if err != nil {
		log.Fatal(err)
	}

	be, err := backend.New(&backend.Config{
		AdminUser:                 helper.AdminUser,
		AdminPassword:             helper.AdminPassword,
		ClientDeactivateThreshold: helper.ClientDeactivateThreshold,
		SnapshotThreshold:         helper.SnapshotThreshold,
		AuthWebhookCacheSize:      helper.AuthWebhookSize,
		ProjectInfoCacheSize:      helper.ProjectInfoCacheSize,
		ProjectInfoCacheTTL:       helper.ProjectInfoCacheTTL.String(),
		AdminTokenDuration:        helper.AdminTokenDuration,
	}, &mongo.Config{
		ConnectionURI:     helper.MongoConnectionURI,
		YorkieDatabase:    helper.TestDBName(),
		ConnectionTimeout: helper.MongoConnectionTimeout,
		PingTimeout:       helper.MongoPingTimeout,
	}, &housekeeping.Config{
		Interval:                  helper.HousekeepingInterval.String(),
		CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
		ProjectFetchSize:          helper.HousekeepingProjectFetchSize,
	}, met)
	if err != nil {
		log.Fatal(err)
	}

	project, err := be.DB.FindProjectInfoByID(
		context.Background(),
		database.DefaultProjectID,
	)
	if err != nil {
		log.Fatal(err)
	}

	testRPCServer, err = rpc.NewServer(&rpc.Config{
		Port: helper.RPCPort,
	}, be)
	if err != nil {
		log.Fatal(err)
	}

	if err := testRPCServer.Start(); err != nil {
		log.Fatalf("failed rpc listen: %s\n", err)
	}

	authInterceptor := client.NewAuthInterceptor(project.PublicKey, "")

	conn := http.DefaultClient
	testClient = v1connect.NewYorkieServiceClient(
		conn,
		"http://"+testRPCAddr,
		connect.WithInterceptors(authInterceptor),
	)

	testAdminAuthInterceptor = admin.NewAuthInterceptor("")

	adminConn := http.DefaultClient
	testAdminClient = v1connect.NewAdminServiceClient(
		adminConn,
		"http://"+testRPCAddr,
		connect.WithInterceptors(testAdminAuthInterceptor),
	)

	code := m.Run()

	if err := be.Shutdown(); err != nil {
		log.Fatal(err)
	}
	testRPCServer.Shutdown(true)
	os.Exit(code)
}

func TestSDKRPCServerBackend(t *testing.T) {
	t.Run("activate/deactivate client test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		_, err = testClient.DeactivateClient(
			context.Background(),
			connect.NewRequest(&api.DeactivateClientRequest{ClientId: activateResp.Msg.ClientId}))
		assert.NoError(t, err)

		// invalid argument
		_, err = testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: ""}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		_, err = testClient.DeactivateClient(
			context.Background(),
			connect.NewRequest(&api.DeactivateClientRequest{ClientId: emptyClientID}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		// client not found
		_, err = testClient.DeactivateClient(
			context.Background(),
			connect.NewRequest(&api.DeactivateClientRequest{ClientId: nilClientID}))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("attach/detach document test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		// try to attach with invalid client ID
		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   invalidClientID,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		// try to attach with invalid client
		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   nilClientID,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		// try to attach already attached document
		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		// try to attach invalid change pack
		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: invalidChangePack,
			},
			))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		// try to detach already detached document
		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: invalidChangePack,
			},
			))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		// document not found
		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: "000000000000000000000000",
				ChangePack: &api.ChangePack{
					Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
				},
			},
			))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		_, err = testClient.DeactivateClient(
			context.Background(),
			connect.NewRequest(&api.DeactivateClientRequest{ClientId: activateResp.Msg.ClientId}))
		assert.NoError(t, err)

		// try to attach the document with a deactivated client
		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("attach/detach on removed document test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		packWithRemoveRequest := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			IsRemoved:   true,
		}

		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.NoError(t, err)

		// try to detach document with same ID as removed document
		// FailedPrecondition because document is not attached.
		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		// try to create new document with same key as removed document
		resPack, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.NoError(t, err)
	})

	t.Run("push/pull changes test", func(t *testing.T) {
		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: helper.TestDocKey(t).String()}))
		assert.NoError(t, err)

		actorID, _ := hex.DecodeString(activateResp.Msg.ClientId)
		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId: activateResp.Msg.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t).String(),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
					Changes: []*api.Change{{
						Id: &api.ChangeID{
							ClientSeq: 1,
							Lamport:   1,
							ActorId:   actorID,
						},
					}},
				},
			},
			))
		assert.NoError(t, err)

		_, err = testClient.PushPullChanges(
			context.Background(),
			connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t).String(),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 2},
					Changes: []*api.Change{{
						Id: &api.ChangeID{
							ClientSeq: 2,
							Lamport:   2,
							ActorId:   actorID,
						},
					}},
				},
			},
			))
		assert.NoError(t, err)

		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t).String(),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 3},
					Changes: []*api.Change{{
						Id: &api.ChangeID{
							ClientSeq: 3,
							Lamport:   3,
							ActorId:   actorID,
						},
					}},
				},
			},
			))
		assert.NoError(t, err)

		// try to push/pull with detached document
		_, err = testClient.PushPullChanges(
			context.Background(),
			connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		// try to push/pull with invalid pack
		_, err = testClient.PushPullChanges(
			context.Background(),
			connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: invalidChangePack,
			},
			))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		_, err = testClient.DeactivateClient(
			context.Background(),
			connect.NewRequest(&api.DeactivateClientRequest{ClientId: activateResp.Msg.ClientId}))
		assert.NoError(t, err)

		// try to push/pull with deactivated client
		_, err = testClient.PushPullChanges(
			context.Background(),
			connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("push/pull on removed document test", func(t *testing.T) {
		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		packWithRemoveRequest := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			IsRemoved:   true,
		}

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: helper.TestDocKey(t).String()}))
		assert.NoError(t, err)

		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.NoError(t, err)

		// try to push/pull on removed document
		_, err = testClient.PushPullChanges(
			context.Background(),
			connect.NewRequest(&api.PushPullChangesRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("remove document test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		packWithRemoveRequest := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			IsRemoved:   true,
		}

		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.NoError(t, err)

		// try to remove removed document
		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("remove document with invalid client state test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		packWithRemoveRequest := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			IsRemoved:   true,
		}

		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testClient.DetachDocument(
			context.Background(),
			connect.NewRequest(&api.DetachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		// try to remove detached document
		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

		_, err = testClient.DeactivateClient(
			context.Background(),
			connect.NewRequest(&api.DeactivateClientRequest{ClientId: activateResp.Msg.ClientId}))
		assert.NoError(t, err)

		// try to remove document with a deactivated client
		_, err = testClient.RemoveDocument(
			context.Background(),
			connect.NewRequest(&api.RemoveDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
				ChangePack: packWithRemoveRequest,
			},
			))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	})

	t.Run("watch document test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t).String(),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		resPack, err := testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		// watch document
		watchResp, err := testClient.WatchDocument(
			context.Background(),
			connect.NewRequest(&api.WatchDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				DocumentId: resPack.Msg.DocumentId,
			},
			))
		assert.NoError(t, err)

		// check if stream is open
		for watchResp.Receive() {
			resp := watchResp.Msg()
			assert.NotNil(t, resp)
			break
		}

		// TODO(krapie): find a way to set timeout for stream
		//// wait for MaxConnectionAge + MaxConnectionAgeGrace
		//time.Sleep(helper.RPCMaxConnectionAge + helper.RPCMaxConnectionAgeGrace)
		//
		//// check if stream has closed by server (EOF)
		//_ = watchResp.Msg()
		//assert.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
		//assert.Contains(t, err.Error(), "EOF")
	})
}

func TestAdminRPCServerBackend(t *testing.T) {
	t.Run("admin signup test", func(t *testing.T) {
		adminUser := helper.TestSlugName(t)
		adminPassword := helper.AdminPassword + "123!"

		_, err := testAdminClient.SignUp(
			context.Background(),
			connect.NewRequest(&api.SignUpRequest{
				Username: adminUser,
				Password: adminPassword,
			},
			))
		assert.NoError(t, err)

		// try to sign up with existing username
		_, err = testAdminClient.SignUp(
			context.Background(),
			connect.NewRequest(&api.SignUpRequest{
				Username: adminUser,
				Password: adminPassword,
			},
			))
		assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
	})

	t.Run("admin login test", func(t *testing.T) {
		_, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		// try to log in with invalid password
		_, err = testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: invalidSlugName,
			},
			))
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("admin create project test", func(t *testing.T) {
		projectName := helper.TestSlugName(t)

		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		_, err = testAdminClient.CreateProject(
			context.Background(),
			connect.NewRequest(&api.CreateProjectRequest{
				Name: projectName,
			},
			))
		assert.NoError(t, err)

		// try to create project with existing name
		_, err = testAdminClient.CreateProject(
			context.Background(),
			connect.NewRequest(&api.CreateProjectRequest{
				Name: projectName,
			},
			))
		assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
	})

	t.Run("admin list projects test", func(t *testing.T) {
		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		_, err = testAdminClient.CreateProject(
			context.Background(),
			connect.NewRequest(&api.CreateProjectRequest{
				Name: helper.TestSlugName(t),
			},
			))
		assert.NoError(t, err)

		_, err = testAdminClient.ListProjects(
			context.Background(),
			connect.NewRequest(&api.ListProjectsRequest{}))
		assert.NoError(t, err)
	})

	t.Run("admin get project test", func(t *testing.T) {
		projectName := helper.TestSlugName(t)

		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		_, err = testAdminClient.CreateProject(
			context.Background(),
			connect.NewRequest(&api.CreateProjectRequest{
				Name: projectName,
			},
			))
		assert.NoError(t, err)

		_, err = testAdminClient.GetProject(
			context.Background(),
			connect.NewRequest(&api.GetProjectRequest{
				Name: projectName,
			},
			))
		assert.NoError(t, err)

		// try to get project with non-existing name
		_, err = testAdminClient.GetProject(
			context.Background(),
			connect.NewRequest(&api.GetProjectRequest{
				Name: invalidSlugName,
			},
			))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("admin update project test", func(t *testing.T) {
		projectName := helper.TestSlugName(t)

		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		createResp, err := testAdminClient.CreateProject(
			context.Background(),
			connect.NewRequest(&api.CreateProjectRequest{
				Name: projectName,
			},
			))
		assert.NoError(t, err)

		_, err = testAdminClient.UpdateProject(
			context.Background(),
			connect.NewRequest(&api.UpdateProjectRequest{
				Id: createResp.Msg.Project.Id,
				Fields: &api.UpdatableProjectFields{
					Name: &wrapperspb.StringValue{Value: "updated"},
				},
			},
			))
		assert.NoError(t, err)

		// try to update project with invalid field
		_, err = testAdminClient.UpdateProject(
			context.Background(),
			connect.NewRequest(&api.UpdateProjectRequest{
				Id: projectName,
				Fields: &api.UpdatableProjectFields{
					Name: &wrapperspb.StringValue{Value: invalidSlugName},
				},
			},
			))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("admin list documents test", func(t *testing.T) {
		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		_, err = testAdminClient.ListDocuments(
			context.Background(),
			connect.NewRequest(&api.ListDocumentsRequest{
				ProjectName: defaultProjectName,
			},
			))
		assert.NoError(t, err)

		// try to list documents with non-existing project name
		_, err = testAdminClient.ListDocuments(
			context.Background(),
			connect.NewRequest(&api.ListDocumentsRequest{
				ProjectName: invalidSlugName,
			},
			))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("admin get document test", func(t *testing.T) {
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testAdminClient.GetDocument(
			context.Background(),
			connect.NewRequest(&api.GetDocumentRequest{
				ProjectName: defaultProjectName,
				DocumentKey: testDocumentKey,
			},
			))
		assert.NoError(t, err)

		// try to get document with non-existing document name
		_, err = testAdminClient.GetDocument(
			context.Background(),
			connect.NewRequest(&api.GetDocumentRequest{
				ProjectName: defaultProjectName,
				DocumentKey: invalidChangePack.DocumentKey,
			},
			))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("admin list changes test", func(t *testing.T) {
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			connect.NewRequest(&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
			))
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Msg.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testClient.AttachDocument(
			context.Background(),
			connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activateResp.Msg.ClientId,
				ChangePack: packWithNoChanges,
			},
			))
		assert.NoError(t, err)

		_, err = testAdminClient.ListChanges(
			context.Background(),
			connect.NewRequest(&api.ListChangesRequest{
				ProjectName: defaultProjectName,
				DocumentKey: testDocumentKey,
			},
			))
		assert.NoError(t, err)

		// try to list changes with non-existing document name
		_, err = testAdminClient.ListChanges(
			context.Background(),
			connect.NewRequest(&api.ListChangesRequest{
				ProjectName: defaultProjectName,
				DocumentKey: invalidChangePack.DocumentKey,
			},
			))
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestConfig_Validate(t *testing.T) {
	scenarios := []*struct {
		config   *rpc.Config
		expected error
	}{
		{config: &rpc.Config{Port: -1}, expected: rpc.ErrInvalidRPCPort},
		{config: &rpc.Config{Port: 8080, CertFile: "noSuchCertFile"}, expected: rpc.ErrInvalidCertFile},
		{config: &rpc.Config{Port: 8080, KeyFile: "noSuchKeyFile"}, expected: rpc.ErrInvalidKeyFile},
		// not to use tls
		{config: &rpc.Config{
			Port:     8080,
			CertFile: "",
			KeyFile:  "",
		},
			expected: nil},
		// pass any file existing
		{config: &rpc.Config{
			Port:     8080,
			CertFile: "server_test.go",
			KeyFile:  "server_test.go",
		},
			expected: nil},
	}
	for _, scenario := range scenarios {
		assert.ErrorIs(t, scenario.config.Validate(), scenario.expected, "provided config: %#v", scenario.config)
	}
}
