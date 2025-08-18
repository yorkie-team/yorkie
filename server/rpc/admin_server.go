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
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/internal/version"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/interceptors"
	"github.com/yorkie-team/yorkie/server/schemas"
	"github.com/yorkie-team/yorkie/server/users"
)

type adminServer struct {
	backend           *backend.Backend
	tokenManager      *auth.TokenManager
	yorkieInterceptor *interceptors.YorkieServiceInterceptor
}

// newAdminServer creates a new instance of adminServer.
func newAdminServer(
	be *backend.Backend,
	tokenManager *auth.TokenManager,
	yorkieInterceptor *interceptors.YorkieServiceInterceptor,
) *adminServer {
	return &adminServer{
		backend:           be,
		tokenManager:      tokenManager,
		yorkieInterceptor: yorkieInterceptor,
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
	project, err := s.GetProjectWithAuth(ctx, req.Msg.Name)
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

// GetProjectStats gets the project stats.
func (s *adminServer) GetProjectStats(
	ctx context.Context,
	req *connect.Request[api.GetProjectStatsRequest],
) (*connect.Response[api.GetProjectStatsResponse], error) {
	from, to, err := converter.FromDateRange(req.Msg.DateRange)
	if err != nil {
		return nil, err
	}

	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	stats, err := projects.GetProjectStats(ctx, s.backend, project.ID, from, to)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetProjectStatsResponse{
		ActiveUsers:      converter.ToMetricPoints(stats.ActiveUsers),
		ActiveUsersCount: int32(stats.ActiveUsersCount),
		DocumentsCount:   stats.DocumentsCount,
		ClientsCount:     stats.ClientsCount,
	}), nil
}

// CreateDocument creates a new document.
func (s *adminServer) CreateDocument(
	ctx context.Context,
	req *connect.Request[api.CreateDocumentRequest],
) (*connect.Response[api.CreateDocumentResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	var initialRoot yson.Object
	if req.Msg.InitialRoot != "" {
		if err := yson.Unmarshal(req.Msg.InitialRoot, &initialRoot); err != nil {
			return nil, err
		}
	}

	locker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, key.Key(req.Msg.DocumentKey)))
	defer locker.RUnlock()

	doc, err := documents.CreateDocument(
		ctx,
		s.backend,
		project,
		project.Owner,
		key.Key(req.Msg.DocumentKey),
		initialRoot,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreateDocumentResponse{
		Document: converter.ToDocumentSummary(doc),
	}), nil
}

// GetDocument gets the document.
func (s *adminServer) GetDocument(
	ctx context.Context,
	req *connect.Request[api.GetDocumentRequest],
) (*connect.Response[api.GetDocumentResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
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
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
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
		req.Msg.IncludeRoot,
		req.Msg.IncludePresences,
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
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
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

	pbVersionVector, err := converter.ToVersionVector(doc.VersionVector())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetSnapshotMetaResponse{
		Lamport:       doc.Lamport(),
		Snapshot:      snapshot,
		VersionVector: pbVersionVector,
	}), nil
}

// ListDocuments lists documents.
func (s *adminServer) ListDocuments(
	ctx context.Context,
	req *connect.Request[api.ListDocumentsRequest],
) (*connect.Response[api.ListDocumentsResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
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
		req.Msg.IncludeRoot,
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
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
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

// UpdateDocument updates the document.
func (s *adminServer) UpdateDocument(
	ctx context.Context,
	req *connect.Request[api.UpdateDocumentRequest],
) (*connect.Response[api.UpdateDocumentResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	hasRoot := req.Msg.Root != ""
	hasSchemaKey := req.Msg.SchemaKey != ""

	// Determine update mode based on provided parameters
	var updateMode string
	switch {
	case !hasRoot && !hasSchemaKey:
		updateMode = documents.UpdateModeDetachSchema
	case hasRoot && !hasSchemaKey:
		updateMode = documents.UpdateModeRootOnly
	case !hasRoot && hasSchemaKey:
		updateMode = documents.UpdateModeSchemaOnly
	case hasRoot && hasSchemaKey:
		updateMode = documents.UpdateModeBoth
	}

	var root yson.Object
	if hasRoot {
		if err := yson.Unmarshal(req.Msg.Root, &root); err != nil {
			return nil, err
		}
	}

	locker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, key.Key(req.Msg.DocumentKey)))
	defer locker.RUnlock()

	docInfo, err := documents.FindDocInfoByKey(
		ctx,
		s.backend,
		project,
		key.Key(req.Msg.DocumentKey),
	)
	if err != nil {
		return nil, err
	}
	docKey := types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID}

	// Check document attachment status for schema updates
	if updateMode != documents.UpdateModeRootOnly {
		attachLocker := s.backend.Lockers.Locker(documents.DocAttachmentKey(docKey))
		defer attachLocker.Unlock()

		count, err := documents.FindAttachedClientCount(ctx, s.backend, docKey)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, documents.ErrDocumentAttached
		}
	}

	schemaKey := docInfo.Schema
	if hasSchemaKey {
		schemaKey = req.Msg.SchemaKey
	}
	schemaName, schemaVersion, err := converter.FromSchemaKey(schemaKey)
	if err != nil {
		return nil, err
	}
	schema, err := schemas.GetSchema(ctx, s.backend, project.ID, schemaName, schemaVersion)
	if err != nil {
		return nil, err
	}

	doc, err := documents.UpdateDocument(
		ctx,
		s.backend,
		project,
		docInfo,
		root,
		schema,
		updateMode,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.UpdateDocumentResponse{
		Document: converter.ToDocumentSummary(doc),
	}), nil
}

// RemoveDocumentByAdmin removes the document of the given key.
func (s *adminServer) RemoveDocumentByAdmin(
	ctx context.Context,
	req *connect.Request[api.RemoveDocumentByAdminRequest],
) (*connect.Response[api.RemoveDocumentByAdminResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	locker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, key.Key(req.Msg.DocumentKey)))
	defer locker.RUnlock()

	docInfo, err := documents.FindDocInfoByKey(ctx, s.backend, project, key.Key(req.Msg.DocumentKey))
	if err != nil {
		return nil, err
	}

	if err := documents.RemoveDocument(
		ctx, s.backend,
		docInfo.RefKey(),
		req.Msg.Force,
	); err != nil {
		return nil, err
	}

	// TODO(emplam27): Change the publisherID to the actual user ID. This is a temporary solution.
	publisherID := time.InitialActorID
	s.backend.PubSub.Publish(
		ctx,
		publisherID,
		events.DocEvent{
			Type:      events.DocChanged,
			Publisher: publisherID,
			DocRefKey: docInfo.RefKey(),
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
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
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

// CreateSchema creates a new schema.
func (s *adminServer) CreateSchema(
	ctx context.Context,
	req *connect.Request[api.CreateSchemaRequest],
) (*connect.Response[api.CreateSchemaResponse], error) {
	fields := &types.CreateSchemaFields{Name: &req.Msg.SchemaName, Version: &req.Msg.SchemaVersion}
	if err := fields.Validate(); err != nil {
		return nil, err
	}

	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	schema, err := schemas.CreateSchema(
		ctx,
		s.backend,
		project.ID,
		req.Msg.SchemaName,
		int(req.Msg.SchemaVersion),
		req.Msg.SchemaBody,
		converter.FromRules(req.Msg.Rules),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreateSchemaResponse{
		Schema: converter.ToSchema(schema),
	}), nil
}

// GetSchema gets the schema.
func (s *adminServer) GetSchema(
	ctx context.Context,
	req *connect.Request[api.GetSchemaRequest],
) (*connect.Response[api.GetSchemaResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	schema, err := schemas.GetSchema(
		ctx,
		s.backend,
		project.ID,
		req.Msg.SchemaName,
		int(req.Msg.Version),
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetSchemaResponse{
		Schema: converter.ToSchema(schema),
	}), nil
}

// GetSchemas gets the schemas.
func (s *adminServer) GetSchemas(
	ctx context.Context,
	req *connect.Request[api.GetSchemasRequest],
) (*connect.Response[api.GetSchemasResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	schemas, err := schemas.GetSchemas(
		ctx,
		s.backend,
		project.ID,
		req.Msg.SchemaName,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetSchemasResponse{
		Schemas: converter.ToSchemas(schemas),
	}), nil
}

// ListSchemas lists the schemas.
func (s *adminServer) ListSchemas(
	ctx context.Context,
	req *connect.Request[api.ListSchemasRequest],
) (*connect.Response[api.ListSchemasResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	schemas, err := schemas.ListSchemas(
		ctx,
		s.backend,
		project.ID,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ListSchemasResponse{
		Schemas: converter.ToSchemas(schemas),
	}), nil
}

// RemoveSchema removes the schema.
func (s *adminServer) RemoveSchema(
	ctx context.Context,
	req *connect.Request[api.RemoveSchemaRequest],
) (*connect.Response[api.RemoveSchemaResponse], error) {
	project, err := s.GetProjectWithAuth(ctx, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	// Check if the schema is being used by any documents
	schema := fmt.Sprintf("%s@%d", req.Msg.SchemaName, req.Msg.Version)
	isAttached, err := s.backend.DB.IsSchemaAttached(ctx, project.ID, schema)
	if err != nil {
		return nil, err
	}
	if isAttached {
		return nil, fmt.Errorf("schema %s is being used", schema)
	}

	if err = schemas.RemoveSchema(
		ctx,
		s.backend,
		project.ID,
		req.Msg.SchemaName,
		int(req.Msg.Version),
	); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.RemoveSchemaResponse{}), nil
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

// RotateProjectKeys rotates the API keys of the project.
func (s *adminServer) RotateProjectKeys(
	ctx context.Context,
	req *connect.Request[api.RotateProjectKeysRequest],
) (*connect.Response[api.RotateProjectKeysResponse], error) {
	user := users.From(ctx)

	oldProject, newProject, err := projects.RotateProjectKeys(
		ctx,
		s.backend,
		user.ID,
		types.ID(req.Msg.Id),
	)
	if err != nil {
		return nil, err
	}

	// NOTE(hackerwins): Each node maintains its own in-memory project cache.
	// So invalidating the cache on a single node may not be sufficient to
	// ensure consistency across the entire cluster.
	// After introducing broadcasting across the cluster, we need to broadcast
	// the project cache invalidation event to all nodes.
	s.backend.Cache.Project.Remove(oldProject.PublicKey)

	// Return updated project
	return connect.NewResponse(&api.RotateProjectKeysResponse{
		Project: converter.ToProject(newProject),
	}), nil
}

// GetProjectWithAuth fetches the project based on the user's access scope.
func (s *adminServer) GetProjectWithAuth(
	ctx context.Context,
	projectName string,
) (*types.Project, error) {
	user := users.From(ctx)

	switch user.AccessScope {
	case types.AccessScopeProject:
		// Verify project from context for project scope
		ctxProject := projects.From(ctx)
		if ctxProject.Name != projectName {
			return nil, fmt.Errorf(
				"project mismatch: key is for %s, got %s: %w",
				ctxProject.Name,
				projectName,
				auth.ErrUnauthenticated,
			)
		}
		return ctxProject, nil
	case types.AccessScopeAdmin:
		// Fetch project for admin scope
		project, err := projects.GetProject(ctx, s.backend, user.ID, projectName)
		if err != nil {
			return nil, err
		}
		return project, nil
	default:
		return nil, fmt.Errorf("invalid access scope: %s", user.AccessScope)
	}
}
