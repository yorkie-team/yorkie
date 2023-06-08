//go:build amd64

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

package rpc_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	monkey "github.com/undefinedlabs/go-mpatch"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/admin"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestAdminRPCServerBackend(t *testing.T) {
	t.Run("admin signup test", func(t *testing.T) {
		adminUser := helper.TestSlugName(t)
		adminPassword := helper.AdminPassword + "123!"

		_, err := testAdminClient.SignUp(
			context.Background(),
			&api.SignUpRequest{
				Username: adminUser,
				Password: adminPassword,
			},
		)
		assert.NoError(t, err)

		// try to sign up with existing username
		_, err = testAdminClient.SignUp(
			context.Background(),
			&api.SignUpRequest{
				Username: adminUser,
				Password: adminPassword,
			},
		)
		assert.Equal(t, codes.Unknown, status.Convert(err).Code())
	})

	t.Run("admin login test", func(t *testing.T) {
		_, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		// try to log in with invalid password
		_, err = testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: invalidSlugName,
			},
		)
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
	})

	t.Run("admin create project test", func(t *testing.T) {
		projectName := helper.TestSlugName(t)

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		_, err = testAdminClient.CreateProject(
			context.Background(),
			&api.CreateProjectRequest{
				Name: projectName,
			},
		)
		assert.NoError(t, err)

		// try to create project with existing name
		_, err = testAdminClient.CreateProject(
			context.Background(),
			&api.CreateProjectRequest{
				Name: projectName,
			},
		)
		assert.Equal(t, codes.AlreadyExists, status.Convert(err).Code())
	})

	t.Run("admin list projects test", func(t *testing.T) {
		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		_, err = testAdminClient.CreateProject(
			context.Background(),
			&api.CreateProjectRequest{
				Name: helper.TestSlugName(t),
			},
		)
		assert.NoError(t, err)

		_, err = testAdminClient.ListProjects(
			context.Background(),
			&api.ListProjectsRequest{},
		)
		assert.NoError(t, err)
	})

	t.Run("admin get project test", func(t *testing.T) {
		projectName := helper.TestSlugName(t)

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		_, err = testAdminClient.CreateProject(
			context.Background(),
			&api.CreateProjectRequest{
				Name: projectName,
			},
		)
		assert.NoError(t, err)

		_, err = testAdminClient.GetProject(
			context.Background(),
			&api.GetProjectRequest{
				Name: projectName,
			},
		)
		assert.NoError(t, err)

		// try to get project with non-existing name
		_, err = testAdminClient.GetProject(
			context.Background(),
			&api.GetProjectRequest{
				Name: invalidSlugName,
			},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())
	})

	t.Run("admin update project test", func(t *testing.T) {
		projectName := helper.TestSlugName(t)

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		createResp, err := testAdminClient.CreateProject(
			context.Background(),
			&api.CreateProjectRequest{
				Name: projectName,
			},
		)
		assert.NoError(t, err)

		_, err = testAdminClient.UpdateProject(
			context.Background(),
			&api.UpdateProjectRequest{
				Id: createResp.Project.Id,
				Fields: &api.UpdatableProjectFields{
					Name: &types.StringValue{Value: "updated"},
				},
			},
		)
		assert.NoError(t, err)

		// try to update project with invalid field
		_, err = testAdminClient.UpdateProject(
			context.Background(),
			&api.UpdateProjectRequest{
				Id: projectName,
				Fields: &api.UpdatableProjectFields{
					Name: &types.StringValue{Value: invalidSlugName},
				},
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())
	})

	t.Run("admin list documents test", func(t *testing.T) {
		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		_, err = testAdminClient.ListDocuments(
			context.Background(),
			&api.ListDocumentsRequest{
				ProjectName: defaultProjectName,
			},
		)
		assert.NoError(t, err)

		// try to list documents with non-existing project name
		_, err = testAdminClient.ListDocuments(
			context.Background(),
			&api.ListDocumentsRequest{
				ProjectName: invalidSlugName,
			},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())
	})

	t.Run("admin get document test", func(t *testing.T) {
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		_, err = testAdminClient.GetDocument(
			context.Background(),
			&api.GetDocumentRequest{
				ProjectName: defaultProjectName,
				DocumentKey: testDocumentKey,
			},
		)
		assert.NoError(t, err)

		// try to get document with non-existing document name
		_, err = testAdminClient.GetDocument(
			context.Background(),
			&api.GetDocumentRequest{
				ProjectName: defaultProjectName,
				DocumentKey: invalidChangePack.DocumentKey,
			},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())
	})

	t.Run("admin list changes test", func(t *testing.T) {
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		_, err = testAdminClient.ListChanges(
			context.Background(),
			&api.ListChangesRequest{
				ProjectName: defaultProjectName,
				DocumentKey: testDocumentKey,
			},
		)
		assert.NoError(t, err)

		// try to list changes with non-existing document name
		_, err = testAdminClient.ListChanges(
			context.Background(),
			&api.ListChangesRequest{
				ProjectName: defaultProjectName,
				DocumentKey: invalidChangePack.DocumentKey,
			},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())
	})

	t.Run("admin remove document test", func(t *testing.T) {
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		// try to remove non-existing document
		_, err = testAdminClient.RemoveDocumentByAdmin(
			context.Background(),
			&api.RemoveDocumentByAdminRequest{
				ProjectName:           defaultProjectName,
				DocumentKey:           testDocumentKey,
				ForceRemoveIfAttached: false,
			},
		)
		assert.Error(t, err)

		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to remove document already attached to client
		_, err = testAdminClient.RemoveDocumentByAdmin(
			context.Background(),
			&api.RemoveDocumentByAdminRequest{
				ProjectName:           defaultProjectName,
				DocumentKey:           testDocumentKey,
				ForceRemoveIfAttached: false,
			},
		)
		assert.Error(t, err)

		// try to remove document already attached to client with force flag
		_, err = testAdminClient.RemoveDocumentByAdmin(
			context.Background(),
			&api.RemoveDocumentByAdminRequest{
				ProjectName:           defaultProjectName,
				DocumentKey:           testDocumentKey,
				ForceRemoveIfAttached: true,
			},
		)
		assert.NoError(t, err)
	})

	t.Run("admin remove document force flag test", func(t *testing.T) {
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to remove document already attached to client
		_, err = testAdminClient.RemoveDocumentByAdmin(
			context.Background(),
			&api.RemoveDocumentByAdminRequest{
				ProjectName:           defaultProjectName,
				DocumentKey:           testDocumentKey,
				ForceRemoveIfAttached: false,
			},
		)
		assert.Error(t, err)

		// try to remove document already attached to client with force flag
		_, err = testAdminClient.RemoveDocumentByAdmin(
			context.Background(),
			&api.RemoveDocumentByAdminRequest{
				ProjectName:           defaultProjectName,
				DocumentKey:           testDocumentKey,
				ForceRemoveIfAttached: true,
			},
		)
		assert.NoError(t, err)
	})

	t.Run("admin remove document consistence test", func(t *testing.T) {

		testServer, addr := dialTestAdminServer(t)
		defer testServer.Stop()

		testRemoveDocumentAdminClient, err := admin.Dial(addr)
		assert.NoError(t, err)

		// patch RemoveDocumentByAdmin method
		var patch *monkey.Patch
		patch, err = monkey.PatchInstanceMethodByName(
			reflect.TypeOf(testServer.adminServer),
			"RemoveDocumentByAdmin",
			func(
				m *api.UnimplementedAdminServiceServer,
				ctx context.Context,
				req *api.RemoveDocumentByAdminRequest,
			) (*api.RemoveDocumentByAdminResponse, error) {
				assert.NoError(t, patch.Unpatch())
				defer func() {
					assert.NoError(t, patch.Patch())
				}()

				docInfo, err := documents.FindDocInfoByKey(ctx, be, defaultProjectID, key.Key(req.DocumentKey))
				if err != nil {
					return nil, err
				}

				locker, err := be.Coordinator.NewLocker(ctx, packs.PushPullKey(defaultProjectID, docInfo.Key))
				if err != nil {
					return nil, err
				}

				if err := locker.Lock(ctx); err != nil {
					return nil, err
				}
				defer func() {
					if err := locker.Unlock(ctx); err != nil {
						logging.DefaultLogger().Error(err)
					}
				}()

				isAttached, err := documents.IsAttachedDocument(ctx, be, defaultProjectID, docInfo.ID)
				if err != nil {
					return nil, err
				}

				if isAttached && !req.ForceRemoveIfAttached {
					return nil, fmt.Errorf("remove document: document is attached")
				}

				// Wait for other client attach document test.
				time.Sleep(1 * time.Second)

				if err := documents.RemoveDocument(ctx, be, docInfo.ID); err != nil {
					return nil, err
				}

				// skip logic for test

				return &api.RemoveDocumentByAdminResponse{}, nil
			},
		)
		assert.NoError(t, err)

		// patch GetProject method
		patch, err = monkey.PatchInstanceMethodByName(
			reflect.TypeOf(testServer.adminServer),
			"GetProject",
			func(
				m *api.UnimplementedAdminServiceServer,
				ctx context.Context,
				req *api.GetProjectRequest,
			) (*api.GetProjectResponse, error) {
				assert.NoError(t, patch.Unpatch())
				defer func() {
					assert.NoError(t, patch.Patch())
				}()

				testProject := &api.Project{
					Id:                        "",
					Name:                      defaultProjectName,
					AuthWebhookUrl:            "",
					AuthWebhookMethods:        nil,
					ClientDeactivateThreshold: "",
					PublicKey:                 "",
					SecretKey:                 "",
					CreatedAt:                 types.TimestampNow(),
					UpdatedAt:                 types.TimestampNow(),
				}
				return &api.GetProjectResponse{
					Project: testProject,
				}, nil
			},
		)
		assert.NoError(t, err)

		// setup test client and document
		testDocumentKey := helper.TestDocKey(t).String()

		resp, err := testAdminClient.LogIn(
			context.Background(),
			&api.LogInRequest{
				Username: helper.AdminUser,
				Password: helper.AdminPassword,
			},
		)
		assert.NoError(t, err)

		testAdminAuthInterceptor.SetToken(resp.Token)

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: testDocumentKey,
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		attachResp1, err := testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to remove document already attached to client
		err = testRemoveDocumentAdminClient.RemoveDocument(
			context.Background(),
			defaultProjectName,
			testDocumentKey,
			true,
		)
		assert.NoError(t, err)

		// during the mockRemoveDocumentByAdmin running, the document is attached to the client
		// removeDocumentByAdmin should have a lock to prevent the document from being attached to the client
		attachResp2, err := testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// check AttachDocument is called after RemoveDocument for locker
		assert.Equal(t, attachResp1.ClientId, attachResp2.ClientId)
		assert.NotEqual(t, attachResp1.DocumentId, attachResp2.DocumentId)
	})
}
