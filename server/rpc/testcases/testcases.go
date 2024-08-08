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

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
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
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(clients.ErrInvalidClientKey), converter.ErrorCodeOf(err))

	_, err = testClient.DeactivateClient(
		context.Background(),
		connect.NewRequest(&api.DeactivateClientRequest{ClientId: emptyClientID}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(time.ErrInvalidHexString), converter.ErrorCodeOf(err))

	// client not found
	_, err = testClient.DeactivateClient(
		context.Background(),
		connect.NewRequest(&api.DeactivateClientRequest{ClientId: nilClientID}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(database.ErrClientNotFound), converter.ErrorCodeOf(err))
}

// RunAttachAndDetachDocumentTest runs the AttachDocument and DetachDocument test.
func RunAttachAndDetachDocumentTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(time.ErrInvalidHexString), converter.ErrorCodeOf(err))

	// try to attach with invalid client
	_, err = testClient.AttachDocument(
		context.Background(),
		connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   nilClientID,
			ChangePack: packWithNoChanges,
		},
		))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(database.ErrClientNotFound), converter.ErrorCodeOf(err))

	// try to attach already attached document
	_, err = testClient.AttachDocument(
		context.Background(),
		connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   activateResp.Msg.ClientId,
			ChangePack: packWithNoChanges,
		},
		))
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentAlreadyAttached), converter.ErrorCodeOf(err))

	// try to attach invalid change pack
	_, err = testClient.AttachDocument(
		context.Background(),
		connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   activateResp.Msg.ClientId,
			ChangePack: invalidChangePack,
		},
		))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(converter.ErrCheckpointRequired), converter.ErrorCodeOf(err))

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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotAttached), converter.ErrorCodeOf(err))

	_, err = testClient.DetachDocument(
		context.Background(),
		connect.NewRequest(&api.DetachDocumentRequest{
			ClientId:   activateResp.Msg.ClientId,
			ChangePack: invalidChangePack,
		},
		))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(converter.ErrCheckpointRequired), converter.ErrorCodeOf(err))

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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotFound), converter.ErrorCodeOf(err))

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
	assert.Equal(t, connecthelper.CodeOf(database.ErrClientNotActivated), converter.ErrorCodeOf(err))
}

// RunAttachAndDetachRemovedDocumentTest runs the AttachDocument and DetachDocument test on a removed document.
func RunAttachAndDetachRemovedDocumentTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotAttached), converter.ErrorCodeOf(err))

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
}

// RunPushPullChangeTest runs the PushChange and PullChange test.
func RunPushPullChangeTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotAttached), converter.ErrorCodeOf(err))

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
	assert.Equal(t, connecthelper.CodeOf(converter.ErrCheckpointRequired), converter.ErrorCodeOf(err))

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
	assert.Equal(t, connecthelper.CodeOf(database.ErrClientNotActivated), converter.ErrorCodeOf(err))
}

// RunPushPullChangeOnRemovedDocumentTest runs the PushChange and PullChange test on a removed document.
func RunPushPullChangeOnRemovedDocumentTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotAttached), converter.ErrorCodeOf(err))
}

// RunRemoveDocumentTest runs the RemoveDocument test.
func RunRemoveDocumentTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotAttached), converter.ErrorCodeOf(err))
}

// RunRemoveDocumentWithInvalidClientStateTest runs the RemoveDocument test with an invalid client state.
func RunRemoveDocumentWithInvalidClientStateTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotAttached), converter.ErrorCodeOf(err))

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
	assert.Equal(t, connecthelper.CodeOf(database.ErrClientNotActivated), converter.ErrorCodeOf(err))
}

// RunWatchDocumentTest runs the WatchDocument test.
func RunWatchDocumentTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
) {
	activateResp, err := testClient.ActivateClient(
		context.Background(),
		connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
	assert.NoError(t, err)

	docKey := helper.TestDocKey(t).String()

	packWithNoChanges := &api.ChangePack{
		DocumentKey: docKey,
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
}

// RunAdminSignUpTest runs the SignUp test in admin.
func RunAdminSignUpTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
) {
	adminUser := helper.TestSlugName(t)
	adminPassword := helper.AdminPasswordForSignUp

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
	assert.Equal(t, connecthelper.CodeOf(database.ErrUserAlreadyExists), converter.ErrorCodeOf(err))
}

// RunAdminLoginTest runs the Admin Login test.
func RunAdminLoginTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrMismatchedPassword), converter.ErrorCodeOf(err))
}

// RunAdminDeleteAccountTest runs the admin delete user test.
func RunAdminDeleteAccountTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
) {
	adminUser := helper.TestSlugName(t)
	adminPassword := helper.AdminPasswordForSignUp

	_, err := testAdminClient.SignUp(
		context.Background(),
		connect.NewRequest(&api.SignUpRequest{
			Username: adminUser,
			Password: adminPassword,
		},
		))
	assert.NoError(t, err)

	_, err = testAdminClient.DeleteAccount(
		context.Background(),
		connect.NewRequest(&api.DeleteAccountRequest{
			Username: adminUser,
			Password: adminPassword,
		},
		))
	assert.NoError(t, err)

	// try to delete user with not existing username
	_, err = testAdminClient.DeleteAccount(
		context.Background(),
		connect.NewRequest(&api.DeleteAccountRequest{
			Username: adminUser,
			Password: adminPassword,
		},
		))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(database.ErrUserNotFound), converter.ErrorCodeOf(err))
}

// RunAdminChangePasswordTest runs the admin change password user test.
func RunAdminChangePasswordTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
) {
	adminUser := helper.TestSlugName(t)
	adminPassword := helper.AdminPasswordForSignUp

	_, err := testAdminClient.SignUp(
		context.Background(),
		connect.NewRequest(&api.SignUpRequest{
			Username: adminUser,
			Password: adminPassword,
		},
		))
	assert.NoError(t, err)

	_, err = testAdminClient.LogIn(
		context.Background(),
		connect.NewRequest(&api.LogInRequest{
			Username: adminUser,
			Password: adminPassword,
		},
		))
	assert.NoError(t, err)

	newAdminPassword := helper.AdminPassword + "12345!"
	_, err = testAdminClient.ChangePassword(
		context.Background(),
		connect.NewRequest(&api.ChangePasswordRequest{
			Username:        adminUser,
			CurrentPassword: adminPassword,
			NewPassword:     newAdminPassword,
		},
		))
	assert.NoError(t, err)

	// log in fail when try to log in with old password
	_, err = testAdminClient.LogIn(
		context.Background(),
		connect.NewRequest(&api.LogInRequest{
			Username: adminUser,
			Password: adminPassword,
		},
		))
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(database.ErrMismatchedPassword), converter.ErrorCodeOf(err))

	_, err = testAdminClient.LogIn(
		context.Background(),
		connect.NewRequest(&api.LogInRequest{
			Username: adminUser,
			Password: newAdminPassword,
		},
		))
	assert.NoError(t, err)

	// try to change password with invalid password
	_, err = testAdminClient.ChangePassword(
		context.Background(),
		connect.NewRequest(&api.ChangePasswordRequest{
			Username:        adminUser,
			CurrentPassword: adminPassword,
			NewPassword:     invalidSlugName,
		},
		))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// RunAdminCreateProjectTest runs the CreateProject test in admin.
func RunAdminCreateProjectTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrProjectAlreadyExists), converter.ErrorCodeOf(err))
}

// RunAdminListProjectsTest runs the ListProjects test in admin.
func RunAdminListProjectsTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminGetProjectTest runs the GetProject test in admin.
func RunAdminGetProjectTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrProjectNotFound), converter.ErrorCodeOf(err))
}

// RunAdminUpdateProjectTest runs the UpdateProject test in admin.
func RunAdminUpdateProjectTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
}

// RunAdminListDocumentsTest runs the ListDocuments test in admin.
func RunAdminListDocumentsTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrProjectNotFound), converter.ErrorCodeOf(err))
}

// RunAdminGetDocumentTest runs the GetDocument test in admin.
func RunAdminGetDocumentTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotFound), converter.ErrorCodeOf(err))
}

// RunAdminListChangesTest runs the ListChanges test in admin.
func RunAdminListChangesTest(
	t *testing.T,
	testClient v1connect.YorkieServiceClient,
	testAdminClient v1connect.AdminServiceClient,
	testAdminAuthInterceptor *admin.AuthInterceptor,
) {
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
		}),
	)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	assert.Equal(t, connecthelper.CodeOf(database.ErrDocumentNotFound), converter.ErrorCodeOf(err))
}

// RunAdminGetServerVersionTest runs the GetServerVersion test in admin.
func RunAdminGetServerVersionTest(
	t *testing.T,
	testAdminClient v1connect.AdminServiceClient,
) {
	versionResponse, err := testAdminClient.GetServerVersion(
		context.Background(),
		connect.NewRequest(&api.GetServerVersionRequest{}),
	)

	assert.NoError(t, err)
	assert.NotNil(t, versionResponse)

	responseMsg := versionResponse.Msg

	assert.NotEmpty(t, responseMsg.YorkieVersion)
	assert.NotEmpty(t, responseMsg.GoVersion)
	assert.Regexp(t, `^\d+\.\d+\.\d+$`, responseMsg.YorkieVersion)
	assert.Regexp(t, `^go\d+\.\d+(\.\d+)?$`, responseMsg.GoVersion)
}
