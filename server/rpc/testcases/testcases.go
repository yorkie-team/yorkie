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

// Package testcases contains testcases for server
package testcases

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/admin"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	defaultProjectName = "default"
	invalidSlugName    = "@#$%^&*()_+"

	nilClientID     = "000000000000000000000000"
	emptyClientID   = ""
	invalidClientID = "invalid"

	invalidChangePack = &api.ChangePack{
		DocumentKey: "invalid",
		Checkpoint:  nil,
	}
)

// RunActivateAndDeactivateClientTest runs the ActivateClient and DeactivateClient test.
func RunActivateAndDeactivateClientTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := t.Name()
	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
	assert.NoError(t, err)

	_, err = testClient.DeactivateClient(
		context.Background(),
		&api.DeactivateClientRequest{
			ClientId: activateResp.ClientId,
		},
	)
	assert.NoError(t, err)

	// invalid argument
	_, err = testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: ""},
	)
	assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

	_, err = testClient.DeactivateClient(
		context.Background(),
		&api.DeactivateClientRequest{
			ClientId: emptyClientID,
		},
	)
	assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

	// client not found
	_, err = testClient.DeactivateClient(
		context.Background(),
		&api.DeactivateClientRequest{
			ClientId: nilClientID,
		},
	)
	assert.Equal(t, codes.NotFound, status.Convert(err).Code())

}

// RunAttachAndDetachDocumentTest runs the AttachDocument and DetachDocument test.
func RunAttachAndDetachDocumentTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := t.Name()
	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
	assert.NoError(t, err)

	packWithNoChanges := &api.ChangePack{
		DocumentKey: helper.TestDocKey(t).String(),
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
	}

	resPack, err := testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	// try to attach with invalid client ID
	_, err = testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   invalidClientID,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

	// try to attach with invalid client
	_, err = testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   nilClientID,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.NotFound, status.Convert(err).Code())

	// try to attach already attached document
	_, err = testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

	// try to attach invalid change pack
	_, err = testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: invalidChangePack,
		},
	)
	assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	// try to detach already detached document
	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: invalidChangePack,
		},
	)
	assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

	// document not found
	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: "000000000000000000000000",
			ChangePack: &api.ChangePack{
				Checkpoint: &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
			},
		},
	)
	assert.Equal(t, codes.NotFound, status.Convert(err).Code())

	_, err = testClient.DeactivateClient(
		context.Background(),
		&api.DeactivateClientRequest{

			ClientId: activateResp.ClientId,
		},
	)
	assert.NoError(t, err)

	// try to attach the document with a deactivated client
	_, err = testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
}

// RunAttachAndDetachRemovedDocumentTest runs the AttachDocument and DetachDocument test on a removed document.
func RunAttachAndDetachRemovedDocumentTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := t.Name()

	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
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
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.NoError(t, err)

	// try to detach document with same ID as removed document
	// FailedPrecondition because document is not attached.
	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

	// try to create new document with same key as removed document
	resPack, err = testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.NoError(t, err)
}

// RunPushPullChangeTest runs the PushChange and PullChange test.
func RunPushPullChangeTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := helper.TestDocKey(t).String()

	packWithNoChanges := &api.ChangePack{
		DocumentKey: helper.TestDocKey(t).String(),
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
	}

	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
	assert.NoError(t, err)

	actorID, _ := hex.DecodeString(activateResp.ClientId)
	resPack, err := testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId: activateResp.ClientId,
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
	)
	assert.NoError(t, err)

	_, err = testClient.PushPullChanges(
		context.Background(),
		&api.PushPullChangesRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
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
	)
	assert.NoError(t, err)

	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
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
	)
	assert.NoError(t, err)

	// try to push/pull with detached document
	_, err = testClient.PushPullChanges(
		context.Background(),
		&api.PushPullChangesRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

	// try to push/pull with invalid pack
	_, err = testClient.PushPullChanges(
		context.Background(),
		&api.PushPullChangesRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: invalidChangePack,
		},
	)
	assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

	_, err = testClient.DeactivateClient(
		context.Background(),
		&api.DeactivateClientRequest{

			ClientId: activateResp.ClientId,
		},
	)
	assert.NoError(t, err)

	// try to push/pull with deactivated client
	_, err = testClient.PushPullChanges(
		context.Background(),
		&api.PushPullChangesRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
}

// RunPushPullChangeOnRemovedDocumentTest runs the PushChange and PullChange test on a removed document.
func RunPushPullChangeOnRemovedDocumentTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := helper.TestDocKey(t).String()
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
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
	assert.NoError(t, err)

	resPack, err := testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.NoError(t, err)

	// try to push/pull on removed document
	_, err = testClient.PushPullChanges(
		context.Background(),
		&api.PushPullChangesRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
}

// RunRemoveDocumentTest runs the RemoveDocument test.
func RunRemoveDocumentTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := t.Name()

	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
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
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.NoError(t, err)

	// try to remove removed document
	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
}

// RunRemoveDocumentWithInvalidClientStateTest runs the RemoveDocument test with an invalid client state.
func RunRemoveDocumentWithInvalidClientStateTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := t.Name()

	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
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
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	_, err = testClient.DetachDocument(
		context.Background(),
		&api.DetachDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	// try to remove detached document
	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

	_, err = testClient.DeactivateClient(
		context.Background(),
		&api.DeactivateClientRequest{

			ClientId: activateResp.ClientId,
		},
	)
	assert.NoError(t, err)

	// try to remove document with a deactivated client
	_, err = testClient.RemoveDocument(
		context.Background(),
		&api.RemoveDocumentRequest{

			ClientId:   activateResp.ClientId,
			DocumentId: resPack.DocumentId,
			ChangePack: packWithRemoveRequest,
		},
	)
	assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
}

// RunWatchDocumentTest runs the WatchDocument test.
func RunWatchDocumentTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
) {
	clientKey := t.Name()

	activateResp, err := testClient.ActivateClient(
		context.Background(),
		&api.ActivateClientRequest{ClientKey: clientKey},
	)
	assert.NoError(t, err)

	docKey := helper.TestDocKey(t).String()

	packWithNoChanges := &api.ChangePack{
		DocumentKey: docKey,
		Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
	}

	resPack, err := testClient.AttachDocument(
		context.Background(),
		&api.AttachDocumentRequest{

			ClientId:   activateResp.ClientId,
			ChangePack: packWithNoChanges,
		},
	)
	assert.NoError(t, err)

	// watch document
	watchResp, err := testClient.WatchDocument(
		context.Background(),
		&api.WatchDocumentRequest{
			ClientId:    activateResp.ClientId,
			DocumentKey: docKey,
			DocumentId:  resPack.DocumentId,
		},
	)
	assert.NoError(t, err)

	// check if stream is open
	_, err = watchResp.Recv()
	assert.NoError(t, err)

	// wait for MaxConnectionAge + MaxConnectionAgeGrace
	time.Sleep(helper.RPCMaxConnectionAge + helper.RPCMaxConnectionAgeGrace)

	// check if stream has closed by server (EOF)
	_, err = watchResp.Recv()
	assert.Equal(t, codes.Unavailable, status.Code(err))
	assert.Contains(t, err.Error(), "EOF")
}

// RunAdminSignUpTest runs the SignUp test in admin.
func RunAdminSignUpTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
) {
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
	assert.Equal(t, codes.AlreadyExists, status.Convert(err).Code())
}

// RunAdminLoginTest runs the Admin Login test.
func RunAdminLoginTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
) {
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
}

// RunAdminCreateProjectTest runs the CreateProject test in admin.
func RunAdminCreateProjectTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminListProjectsTest runs the ListProjects test in admin.
func RunAdminListProjectsTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminGetProjectTest runs the GetProject test in admin.
func RunAdminGetProjectTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminUpdateProjectTest runs the UpdateProject test in admin.
func RunAdminUpdateProjectTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminListDocumentsTest runs the ListDocuments test in admin.
func RunAdminListDocumentsTest(
	t *testing.T,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminGetDocumentTest runs the GetDocument test in admin.
func RunAdminGetDocumentTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
	clientKey := t.Name()
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
		&api.ActivateClientRequest{ClientKey: clientKey},
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
}

// RunAdminListChangesTest runs the ListChanges test in admin.
func RunAdminListChangesTest(
	t *testing.T,
	testClient api.YorkieServiceClient,
	testAdminClient api.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
	clientKey := t.Name()
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
		&api.ActivateClientRequest{ClientKey: clientKey},
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
}
