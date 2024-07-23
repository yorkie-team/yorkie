/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package rpc

import (
	"context"
	"fmt"
	"runtime"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/internal/version"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/users"
)

type adminServer struct {
	backend      *backend.Backend
	tokenManager *auth.TokenManager
}

// newAdminServer creates a new instance of adminServer.
func newAdminServer(be *backend.Backend, tokenManager *auth.TokenManager) *adminServer {
	return &adminServer{
		backend:      be,
		tokenManager: tokenManager,
	}
}

// SignUp signs up a user.
func (s *adminServer) SignUp(
	ctx context.Context,
	req *connect.Request[api.SignUpRequest],
) (*connect.Response[api.SignUpResponse], error) {
	fields := &types.UserFields{Username: &req.Msg.Username, Password: &req.Msg.Password}
	if err := fields.Validate(); err != nil {
		return nil, err
	}

	user, err := users.SignUp(ctx, s.backend, req.Msg.Username, req.Msg.Password)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.SignUpResponse{
		User: converter.ToUser(user),
	}), nil
}

// LogIn logs in a user.
func (s *adminServer) LogIn(
	ctx context.Context,
	req *connect.Request[api.LogInRequest],
) (*connect.Response[api.LogInResponse], error) {
	user, err := users.IsCorrectPassword(
		ctx,
		s.backend,
		req.Msg.Username,
		req.Msg.Password,
	)
	if err != nil {
		return nil, err
	}

	token, err := s.tokenManager.Generate(user.Username)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.LogInResponse{
		Token: token,
	}), nil
}

// DeleteAccount deletes a user.
func (s *adminServer) DeleteAccount(
	ctx context.Context,
	req *connect.Request[api.DeleteAccountRequest],
) (*connect.Response[api.DeleteAccountResponse], error) {
	user, err := users.IsCorrectPassword(
		ctx,
		s.backend,
		req.Msg.Username,
		req.Msg.Password,
	)
	if err != nil {
		return nil, err
	}

	err = users.DeleteAccountByName(ctx, s.backend, user.Username)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.DeleteAccountResponse{}), nil
}

// ChangePassword changes to new password.
func (s *adminServer) ChangePassword(
	ctx context.Context,
	req *connect.Request[api.ChangePasswordRequest],
) (*connect.Response[api.ChangePasswordResponse], error) {
	fields := &types.UserFields{Username: &req.Msg.Username, Password: &req.Msg.NewPassword}
	if err := fields.Validate(); err != nil {
		return nil, err
	}

	user, err := users.IsCorrectPassword(
		ctx,
		s.backend,
		req.Msg.Username,
		req.Msg.CurrentPassword,
	)
	if err != nil {
		return nil, err
	}

	err = users.ChangePassword(ctx, s.backend, user.Username, req.Msg.NewPassword)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ChangePasswordResponse{}), nil
}

// CreateProject creates a new project.
func (s *adminServer) CreateProject(
	ctx context.Context,
	req *connect.Request[api.CreateProjectRequest],
) (*connect.Response[api.CreateProjectResponse], error) {
	fields := &types.CreateProjectFields{Name: &req.Msg.Name}
	if err := fields.Validate(); err != nil {
		return nil, err
	}

	user := users.From(ctx)
	project, err := projects.CreateProject(ctx, s.backend, user.ID, req.Msg.Name)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreateProjectResponse{
		Project: converter.ToProject(project),
	}), nil
}

// ListProjects lists all projects.
func (s *adminServer) ListProjects(
	ctx context.Context,
	_ *connect.Request[api.ListProjectsRequest],
) (*connect.Response[api.ListProjectsResponse], error) {
	user := users.From(ctx)
	projectList, err := projects.ListProjects(ctx, s.backend, user.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ListProjectsResponse{
		Projects: converter.ToProjects(projectList),
	}), nil
}

// GetProject gets a project.
func (s *adminServer) GetProject(
	ctx context.Context,
	req *connect.Request[api.GetProjectRequest],
) (*connect.Response[api.GetProjectResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.Name)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetProjectResponse{
		Project: converter.ToProject(project),
	}), nil
}

// UpdateProject updates the project.
func (s *adminServer) UpdateProject(
	ctx context.Context,
	req *connect.Request[api.UpdateProjectRequest],
) (*connect.Response[api.UpdateProjectResponse], error) {
	fields, err := converter.FromUpdatableProjectFields(req.Msg.Fields)
	if err != nil {
		return nil, err
	}
	if err = fields.Validate(); err != nil {
		return nil, err
	}

	user := users.From(ctx)
	project, err := projects.UpdateProject(
		ctx,
		s.backend,
		user.ID,
		types.ID(req.Msg.Id),
		fields,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.UpdateProjectResponse{
		Project: converter.ToProject(project),
	}), nil
}

// GetDocument gets the document.
func (s *adminServer) GetDocument(
	ctx context.Context,
	req *connect.Request[api.GetDocumentRequest],
) (*connect.Response[api.GetDocumentResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	document, err := documents.GetDocumentSummary(
		ctx,
		s.backend,
		project,
		key.Key(req.Msg.DocumentKey),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetDocumentResponse{
		Document: converter.ToDocumentSummary(document),
	}), nil
}

// GetDocuments gets the documents.
func (s *adminServer) GetDocuments(
	ctx context.Context,
	req *connect.Request[api.GetDocumentsRequest],
) (*connect.Response[api.GetDocumentsResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	var keys []key.Key
	for _, k := range req.Msg.DocumentKeys {
		keys = append(keys, key.Key(k))
	}

	docs, err := documents.GetDocumentSummaries(
		ctx,
		s.backend,
		project,
		keys,
		req.Msg.IncludeSnapshot,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetDocumentsResponse{
		Documents: converter.ToDocumentSummaries(docs),
	}), nil
}

// GetSnapshotMeta gets the snapshot metadata that corresponds to the server sequence.
func (s *adminServer) GetSnapshotMeta(
	ctx context.Context,
	req *connect.Request[api.GetSnapshotMetaRequest],
) (*connect.Response[api.GetSnapshotMetaResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	doc, err := documents.GetDocumentByServerSeq(
		ctx,
		s.backend,
		project,
		key.Key(req.Msg.DocumentKey),
		req.Msg.ServerSeq,
	)
	if err != nil {
		return nil, err
	}

	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetSnapshotMetaResponse{
		Lamport:  doc.Lamport(),
		Snapshot: snapshot,
	}), nil
}

// ListDocuments lists documents.
func (s *adminServer) ListDocuments(
	ctx context.Context,
	req *connect.Request[api.ListDocumentsRequest],
) (*connect.Response[api.ListDocumentsResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	docs, err := documents.ListDocumentSummaries(
		ctx,
		s.backend,
		project,
		types.Paging[types.ID]{
			Offset:    types.ID(req.Msg.PreviousId),
			PageSize:  int(req.Msg.PageSize),
			IsForward: req.Msg.IsForward,
		},
		req.Msg.IncludeSnapshot,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ListDocumentsResponse{
		Documents: converter.ToDocumentSummaries(docs),
	}), nil
}

// SearchDocuments searches documents for a specified string.
func (s *adminServer) SearchDocuments(
	ctx context.Context,
	req *connect.Request[api.SearchDocumentsRequest],
) (*connect.Response[api.SearchDocumentsResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	result, err := documents.SearchDocumentSummaries(
		ctx,
		s.backend,
		project,
		req.Msg.Query,
		int(req.Msg.PageSize),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.SearchDocumentsResponse{
		TotalCount: int32(result.TotalCount),
		Documents:  converter.ToDocumentSummaries(result.Elements),
	}), nil
}

// RemoveDocumentByAdmin removes the document of the given key.
func (s *adminServer) RemoveDocumentByAdmin(
	ctx context.Context,
	req *connect.Request[api.RemoveDocumentByAdminRequest],
) (*connect.Response[api.RemoveDocumentByAdminResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	docInfo, err := documents.FindDocInfoByKey(ctx, s.backend, project, key.Key(req.Msg.DocumentKey))
	if err != nil {
		return nil, err
	}

	// TODO(hackerwins): Rename PushPullKey to something else like DocWriteLockKey?.
	locker, err := s.backend.Coordinator.NewLocker(ctx, packs.PushPullKey(project.ID, docInfo.Key))
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

	if err := documents.RemoveDocument(
		ctx, s.backend,
		docInfo.RefKey(),
		req.Msg.Force,
	); err != nil {
		return nil, err
	}

	// TODO(emplam27): Change the publisherID to the actual user ID. This is a temporary solution.
	publisherID := time.InitialActorID
	s.backend.Coordinator.Publish(
		ctx,
		publisherID,
		sync.DocEvent{
			Type:           types.DocumentChangedEvent,
			Publisher:      publisherID,
			DocumentRefKey: docInfo.RefKey(),
		},
	)

	logging.DefaultLogger().Info(
		fmt.Sprintf("document remove success(projectID: %s, docKey: %s)", project.ID, req.Msg.DocumentKey),
	)

	return connect.NewResponse(&api.RemoveDocumentByAdminResponse{}), nil
}

// ListChanges lists of changes for the given document.
func (s *adminServer) ListChanges(
	ctx context.Context,
	req *connect.Request[api.ListChangesRequest],
) (*connect.Response[api.ListChangesResponse], error) {
	user := users.From(ctx)
	project, err := projects.GetProject(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	docInfo, err := documents.FindDocInfoByKey(
		ctx,
		s.backend,
		project,
		key.Key(req.Msg.DocumentKey),
	)
	if err != nil {
		return nil, err
	}
	lastSeq := docInfo.ServerSeq

	from, to := types.GetChangesRange(types.Paging[int64]{
		Offset:    req.Msg.PreviousSeq,
		PageSize:  int(req.Msg.PageSize),
		IsForward: req.Msg.IsForward,
	}, lastSeq)

	changes, err := packs.FindChanges(
		ctx,
		s.backend,
		docInfo,
		from,
		to,
	)
	if err != nil {
		return nil, err
	}

	pbChanges, err := converter.ToChanges(changes)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ListChangesResponse{
		Changes: pbChanges,
	}), nil
}

// GetServerVersion get the version of yorkie server.
func (s *adminServer) GetServerVersion(
	_ context.Context,
	_ *connect.Request[api.GetServerVersionRequest],
) (*connect.Response[api.GetServerVersionResponse], error) {
	return connect.NewResponse(&api.GetServerVersionResponse{
		YorkieVersion: version.Version,
		GoVersion:     runtime.Version(),
		BuildDate:     version.BuildDate,
	}), nil
}
