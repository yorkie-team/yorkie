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
	"strings"
	"sync"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/internal/version"
	pkgchannel "github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/authz"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/channel"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/invites"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/members"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/revisions"
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
	user := users.From(ctx)
	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.Name)
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

	if err := s.backend.BroadcastCacheInvalidation(
		ctx,
		types.CacheTypeProject,
		project.ID.String(),
	); err != nil {
		logging.From(ctx).Warnf("failed to broadcast cache invalidation: %v", err)
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

	project := projects.From(ctx)

	stats, err := projects.GetProjectStats(ctx, s.backend, project.ID, from, to)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.GetProjectStatsResponse{
		DocumentsCount:              stats.DocumentsCount,
		ClientsCount:                stats.ClientsCount,
		ChannelsCount:               stats.ChannelsCount,
		ActiveUsers:                 converter.ToMetricPoints(stats.ActiveUsers),
		ActiveUsersCount:            int32(stats.ActiveUsersCount),
		ActiveDocuments:             converter.ToMetricPoints(stats.ActiveDocuments),
		ActiveDocumentsCount:        int32(stats.ActiveDocumentsCount),
		ActiveChannels:              converter.ToMetricPoints(stats.ActiveChannels),
		ActiveChannelsCount:         int32(stats.ActiveChannelsCount),
		Sessions:                    converter.ToMetricPoints(stats.Sessions),
		SessionsCount:               int32(stats.SessionsCount),
		PeakSessionsPerChannel:      converter.ToMetricPoints(stats.PeakSessionsPerChannel),
		PeakSessionsPerChannelCount: int32(stats.PeakSessionsPerChannelCount),
	}), nil
}

// RemoveMember removes a member from the project.
func (s *adminServer) RemoveMember(
	ctx context.Context,
	req *connect.Request[api.RemoveMemberRequest],
) (*connect.Response[api.RemoveMemberResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	// Only owner and admin can remove members
	if err := authz.CheckPermission(ctx, s.backend, user.ID, project.ID, database.Admin); err != nil {
		return nil, err
	}

	if err := members.Remove(
		ctx,
		s.backend,
		project.ID,
		req.Msg.Username,
	); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.RemoveMemberResponse{}), nil
}

// ListMembers lists all members of the project.
func (s *adminServer) ListMembers(
	ctx context.Context,
	req *connect.Request[api.ListMembersRequest],
) (*connect.Response[api.ListMembersResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	memberList, err := members.List(ctx, s.backend, project.ID)
	if err != nil {
		return nil, err
	}

	// Prepend owner to the member list
	ownerInfo, err := s.backend.DB.FindUserInfoByID(ctx, project.Owner)
	if err != nil {
		return nil, err
	}

	owner := &types.Member{
		ID:        project.Owner,
		ProjectID: project.ID,
		UserID:    project.Owner,
		Username:  ownerInfo.Username,
		Role:      "owner",
		InvitedAt: project.CreatedAt,
	}
	memberList = append([]*types.Member{owner}, memberList...)

	return connect.NewResponse(&api.ListMembersResponse{
		Members: converter.ToMembers(memberList),
	}), nil
}

// UpdateMemberRole updates the role of a project member.
func (s *adminServer) UpdateMemberRole(
	ctx context.Context,
	req *connect.Request[api.UpdateMemberRoleRequest],
) (*connect.Response[api.UpdateMemberRoleResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	// Only owner and admin can update member roles
	if err := authz.CheckPermission(ctx, s.backend, user.ID, project.ID, database.Admin); err != nil {
		return nil, err
	}

	member, err := members.UpdateRole(
		ctx,
		s.backend,
		project.ID,
		req.Msg.Username,
		req.Msg.Role,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.UpdateMemberRoleResponse{
		Member: converter.ToMember(member),
	}), nil
}

// CreateInvite creates a reusable invite for the project.
func (s *adminServer) CreateInvite(
	ctx context.Context,
	req *connect.Request[api.CreateInviteRequest],
) (*connect.Response[api.CreateInviteResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	// Only owner and admin can create invites
	if err := authz.CheckPermission(ctx, s.backend, user.ID, project.ID, database.Admin); err != nil {
		return nil, err
	}

	role, err := database.NewMemberRole(req.Msg.Role)
	if err != nil {
		return nil, err
	}

	var opt invites.ExpireOption
	switch req.Msg.ExpireOption {
	case api.InviteExpireOption_INVITE_EXPIRE_OPTION_ONE_HOUR:
		opt = invites.ExpireOneHour
	case api.InviteExpireOption_INVITE_EXPIRE_OPTION_TWENTY_FOUR_HOURS:
		opt = invites.ExpireTwentyFourHours
	case api.InviteExpireOption_INVITE_EXPIRE_OPTION_SEVEN_DAYS:
		opt = invites.ExpireSevenDays
	default:
		return nil, invites.ErrInvalidInviteExpireOpt
	}

	token, _, err := invites.Create(ctx, s.backend, project.ID, role, user.ID, opt)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.CreateInviteResponse{
		Token: token,
	}), nil
}

// AcceptInvite accepts a reusable invite token and adds the user as a member of the project.
func (s *adminServer) AcceptInvite(
	ctx context.Context,
	req *connect.Request[api.AcceptInviteRequest],
) (*connect.Response[api.AcceptInviteResponse], error) {
	user := users.From(ctx)

	member, err := invites.Accept(ctx, s.backend, req.Msg.Token, user.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.AcceptInviteResponse{
		Member: converter.ToMember(member),
	}), nil
}

// CreateDocument creates a new document.
func (s *adminServer) CreateDocument(
	ctx context.Context,
	req *connect.Request[api.CreateDocumentRequest],
) (*connect.Response[api.CreateDocumentResponse], error) {
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
			return nil, packs.ErrDocumentAttached
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
	project := projects.From(ctx)

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
			Type:  events.DocChanged,
			Actor: publisherID,
			Key:   docInfo.RefKey(),
		},
	)

	logging.DefaultLogger().Info(
		fmt.Sprintf("document remove success(projectID: %s, docKey: %s)", project.ID, req.Msg.DocumentKey),
	)

	return connect.NewResponse(&api.RemoveDocumentByAdminResponse{}), nil
}

// ListChannels lists channels for the given project.
func (s *adminServer) ListChannels(
	ctx context.Context,
	req *connect.Request[api.ListChannelsRequest],
) (*connect.Response[api.ListChannelsResponse], error) {
	limit := int(req.Msg.Limit)
	if limit <= 0 {
		return nil, errors.InvalidArgument("limit must be greater than 0")
	}
	if limit > channel.MaxChannelLimit {
		return nil, errors.InvalidArgument(fmt.Sprintf("limit must be less than or equal to %d", channel.MaxChannelLimit))
	}

	project := projects.From(ctx)
	channels, err := s.backend.BroadcastChannelList(
		ctx,
		project.ID,
		req.Msg.Query,
		limit,
	)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.ListChannelsResponse{
		Channels: converter.ToChannelSummaries(channels),
	}), nil
}

// GetChannels gets the channels for the given keys.
func (s *adminServer) GetChannels(
	ctx context.Context,
	req *connect.Request[api.GetChannelsRequest],
) (*connect.Response[api.GetChannelsResponse], error) {
	project := projects.From(ctx)
	includeSubPath := req.Msg.IncludeSubPath

	clusterClient, err := s.backend.ClusterClient()
	if err != nil {
		return nil, err
	}

	// Group channel keys by their first key path for istio consistent hash sharding.
	groups := groupByFirstKeyPath(req.Msg.ChannelKeys)

	var wg sync.WaitGroup
	var mu sync.Mutex
	channels := make([]*types.ChannelSummary, 0)
	var firstErr error

	for firstPath, keys := range groups {
		wg.Add(1)
		go func(fp string, ks []key.Key) {
			defer wg.Done()

			result, err := clusterClient.GetChannels(ctx, project, ks, fp, includeSubPath)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}

			mu.Lock()
			channels = append(channels, result...)
			mu.Unlock()
		}(firstPath, keys)
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return connect.NewResponse(&api.GetChannelsResponse{
		Channels: converter.ToChannelSummaries(channels),
	}), nil
}

// groupByFirstKeyPath groups channel keys by their first key path segment.
func groupByFirstKeyPath(channelKeys []string) map[string][]key.Key {
	groups := make(map[string][]key.Key)

	for _, keyStr := range channelKeys {
		k := key.Key(keyStr)
		paths := strings.Split(keyStr, pkgchannel.ChannelKeyPathSeparator)
		if len(paths) == 0 {
			continue
		}
		firstPath := paths[0]
		groups[firstPath] = append(groups[firstPath], k)
	}

	return groups
}

// ListChanges lists of changes for the given document.
func (s *adminServer) ListChanges(
	ctx context.Context,
	req *connect.Request[api.ListChangesRequest],
) (*connect.Response[api.ListChangesResponse], error) {
	project := projects.From(ctx)

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

	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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
	project := projects.From(ctx)

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

	project, prev, err := projects.RotateProjectKeys(
		ctx,
		s.backend,
		user.ID,
		types.ID(req.Msg.Id),
	)
	if err != nil {
		return nil, err
	}

	if err := s.backend.BroadcastCacheInvalidation(
		ctx,
		types.CacheTypeProject,
		prev.ID.String(),
	); err != nil {
		logging.From(ctx).Warnf("failed to broadcast cache invalidation: %v", err)
	}

	// Return updated project
	return connect.NewResponse(&api.RotateProjectKeysResponse{
		Project: converter.ToProject(project),
	}), nil
}

// ListRevisionsByAdmin returns a list of revisions for the given document.
func (s *adminServer) ListRevisionsByAdmin(
	ctx context.Context,
	req *connect.Request[api.ListRevisionsByAdminRequest],
) (*connect.Response[api.ListRevisionsByAdminResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	docInfo, err := documents.FindDocInfoByKey(ctx, s.backend, project, key.Key(req.Msg.DocumentKey))
	if err != nil {
		return nil, err
	}

	docRefKey := types.DocRefKey{
		ProjectID: project.ID,
		DocID:     docInfo.ID,
	}

	paging := types.Paging[int]{
		Offset:    int(req.Msg.Offset),
		PageSize:  int(req.Msg.PageSize),
		IsForward: req.Msg.IsForward,
	}

	revisions, err := s.backend.DB.FindRevisionInfosByPaging(ctx, docRefKey, paging, false)
	if err != nil {
		return nil, err
	}

	var pbRevisions []*api.RevisionSummary
	for _, rev := range revisions {
		pbRevisions = append(pbRevisions, converter.ToRevisionSummary(rev.ToTypesRevisionSummary()))
	}

	return connect.NewResponse(&api.ListRevisionsByAdminResponse{
		Revisions:  pbRevisions,
		TotalCount: int32(len(pbRevisions)),
	}), nil
}

// GetRevisionByAdmin returns a specific revision with its full snapshot data.
func (s *adminServer) GetRevisionByAdmin(
	ctx context.Context,
	req *connect.Request[api.GetRevisionByAdminRequest],
) (*connect.Response[api.GetRevisionByAdminResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	docInfo, err := documents.FindDocInfoByKey(ctx, s.backend, project, key.Key(req.Msg.DocumentKey))
	if err != nil {
		return nil, err
	}

	revision, err := s.backend.DB.FindRevisionInfoByID(ctx, types.ID(req.Msg.RevisionId))
	if err != nil {
		return nil, err
	}

	// Verify revision belongs to this document
	if revision.ProjectID != project.ID || revision.DocID != docInfo.ID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("revision not found"))
	}

	return connect.NewResponse(&api.GetRevisionByAdminResponse{
		Revision: converter.ToRevisionSummary(revision.ToTypesRevisionSummary()),
	}), nil
}

// RestoreRevisionByAdmin restores a document to a specific revision by admin.
func (s *adminServer) RestoreRevisionByAdmin(
	ctx context.Context,
	req *connect.Request[api.RestoreRevisionByAdminRequest],
) (*connect.Response[api.RestoreRevisionByAdminResponse], error) {
	user := users.From(ctx)

	project, _, err := projects.ProjectAndRole(ctx, s.backend, user.ID, req.Msg.ProjectName)
	if err != nil {
		return nil, err
	}

	// Only owner and admin can restore revisions
	if err := authz.CheckPermission(ctx, s.backend, user.ID, project.ID, database.Admin); err != nil {
		return nil, err
	}

	docInfo, err := documents.FindDocInfoByKey(ctx, s.backend, project, key.Key(req.Msg.DocumentKey))
	if err != nil {
		return nil, err
	}

	locker := s.backend.Lockers.LockerWithRLock(packs.DocKey(project.ID, key.Key(req.Msg.DocumentKey)))
	defer locker.RUnlock()

	revision, err := s.backend.DB.FindRevisionInfoByID(ctx, types.ID(req.Msg.RevisionId))
	if err != nil {
		return nil, err
	}

	// Verify revision belongs to this document
	if revision.ProjectID != project.ID || revision.DocID != docInfo.ID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("revision not found"))
	}

	if err := revisions.Restore(ctx, s.backend, project, revision.ID); err != nil {
		return nil, err
	}

	return connect.NewResponse(&api.RestoreRevisionByAdminResponse{}), nil
}
