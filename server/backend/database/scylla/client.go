/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package scylla

import (
	"context"
	"fmt"
	gotime "time"

	"github.com/gocql/gocql"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Ensure Client implements database.Database at compile time.
var _ database.Database = (*Client)(nil)

// Client is a database client that uses ScyllaDB for the table groups enabled
// in Config.Tables and delegates the rest to MongoDB. The default groups
// (clients, changes, snapshots, version vectors) are the write-heavy
// per-document/per-client paths; the remaining metadata-style collections
// (users, projects, members, invites, documents, schemas, revisions, cluster
// nodes) always stay on MongoDB.
type Client struct {
	session *gocql.Session
	mongo   *mongo.Client
	tables  Tables

	// Caches for ScyllaDB-owned data
	clientCache   *cache.LRU[types.ClientRefKey, *database.ClientInfo]
	changeCache   *cache.LRU[types.DocRefKey, *mongo.ChangeStore]
	presenceCache *cache.LRU[types.DocRefKey, *mongo.ChangeStore]
	vectorCache   *cache.LRU[types.DocRefKey, *cmap.Map[types.ID, time.VersionVector]]
}

// Dial creates a new ScyllaDB client with MongoDB delegation for unimplemented methods.
func Dial(scyllaConf *Config, mongoConf *mongo.Config) (*Client, error) {
	// 1. Connect to ScyllaDB
	scyllaSession, err := DialSession(scyllaConf)
	if err != nil {
		return nil, fmt.Errorf("dial ScyllaDB: %w", err)
	}

	// 2. Connect to MongoDB for the table groups not hosted on ScyllaDB.
	mongoClient, err := mongo.Dial(mongoConf)
	if err != nil {
		scyllaSession.Close()
		return nil, fmt.Errorf("dial MongoDB for delegation: %w", err)
	}

	// 3. Create caches for ScyllaDB-owned data
	clientCache, err := cache.NewLRU[types.ClientRefKey, *database.ClientInfo](
		mongoConf.ClientCacheSize, "scylla-clients",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize client cache: %w", err)
	}

	changeCache, err := cache.NewLRU[types.DocRefKey, *mongo.ChangeStore](
		mongoConf.ChangeCacheSize, "scylla-changes",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize change cache: %w", err)
	}

	presenceCache, err := cache.NewLRU[types.DocRefKey, *mongo.ChangeStore](
		mongoConf.ChangeCacheSize, "scylla-presences",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize presence cache: %w", err)
	}

	vectorCache, err := cache.NewLRU[types.DocRefKey, *cmap.Map[types.ID, time.VersionVector]](
		mongoConf.VectorCacheSize, "scylla-vectors",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize vector cache: %w", err)
	}

	logging.DefaultLogger().Infof(
		"ScyllaDB client created, hosts=%v, keyspace=%s, tables=%+v",
		scyllaConf.Hosts,
		scyllaConf.Keyspace,
		scyllaConf.Tables,
	)

	return &Client{
		session:       scyllaSession.Session,
		mongo:         mongoClient,
		tables:        scyllaConf.Tables,
		clientCache:   clientCache,
		changeCache:   changeCache,
		presenceCache: presenceCache,
		vectorCache:   vectorCache,
	}, nil
}

// Close closes all resources of this client.
func (c *Client) Close() error {
	c.session.Close()

	c.clientCache.Purge()
	c.changeCache.Purge()
	c.presenceCache.Purge()
	c.vectorCache.Purge()

	return c.mongo.Close()
}

// InvalidateCache invalidates the cache of the given type and key.
func (c *Client) InvalidateCache(cacheType types.CacheType, key string) {
	c.mongo.InvalidateCache(cacheType, key)
}

// ===== Delegated methods (MongoDB) =====

func (c *Client) TryLeadership(
	ctx context.Context, rpcAddr, leaseToken string, leaseDuration gotime.Duration,
) (*database.ClusterNodeInfo, error) {
	return c.mongo.TryLeadership(ctx, rpcAddr, leaseToken, leaseDuration)
}

func (c *Client) RemoveClusterNode(ctx context.Context, rpcAddr string) error {
	return c.mongo.RemoveClusterNode(ctx, rpcAddr)
}

func (c *Client) RemoveClusterNodes(ctx context.Context) error {
	return c.mongo.RemoveClusterNodes(ctx)
}

func (c *Client) FindClusterNodes(ctx context.Context, window gotime.Duration) ([]*database.ClusterNodeInfo, error) {
	return c.mongo.FindClusterNodes(ctx, window)
}

func (c *Client) FindProjectInfoByPublicKey(ctx context.Context, publicKey string) (*database.ProjectInfo, error) {
	return c.mongo.FindProjectInfoByPublicKey(ctx, publicKey)
}

func (c *Client) FindProjectInfoBySecretKey(ctx context.Context, secretKey string) (*database.ProjectInfo, error) {
	return c.mongo.FindProjectInfoBySecretKey(ctx, secretKey)
}

func (c *Client) FindProjectInfoByName(ctx context.Context, name string) (*database.ProjectInfo, error) {
	return c.mongo.FindProjectInfoByName(ctx, name)
}

func (c *Client) FindProjectInfoByID(ctx context.Context, id types.ID) (*database.ProjectInfo, error) {
	return c.mongo.FindProjectInfoByID(ctx, id)
}

func (c *Client) EnsureDefaultUserAndProject(
	ctx context.Context, username, password string,
) (*database.UserInfo, *database.ProjectInfo, error) {
	return c.mongo.EnsureDefaultUserAndProject(ctx, username, password)
}

func (c *Client) CreateProjectInfo(ctx context.Context, name string, owner types.ID) (*database.ProjectInfo, error) {
	return c.mongo.CreateProjectInfo(ctx, name, owner)
}

func (c *Client) ListProjectInfos(ctx context.Context, owner types.ID) ([]*database.ProjectInfo, error) {
	return c.mongo.ListProjectInfos(ctx, owner)
}

func (c *Client) UpdateProjectInfo(
	ctx context.Context, id types.ID, fields *types.UpdatableProjectFields,
) (*database.ProjectInfo, error) {
	return c.mongo.UpdateProjectInfo(ctx, id, fields)
}

func (c *Client) RotateProjectKeys(
	ctx context.Context, id types.ID, publicKey, secretKey string,
) (*database.ProjectInfo, *database.ProjectInfo, error) {
	return c.mongo.RotateProjectKeys(ctx, id, publicKey, secretKey)
}

func (c *Client) CreateUserInfo(ctx context.Context, username, hashedPassword string) (*database.UserInfo, error) {
	return c.mongo.CreateUserInfo(ctx, username, hashedPassword)
}

func (c *Client) GetOrCreateUserInfoByGitHubID(ctx context.Context, githubID string) (*database.UserInfo, error) {
	return c.mongo.GetOrCreateUserInfoByGitHubID(ctx, githubID)
}

func (c *Client) DeleteUserInfoByName(ctx context.Context, username string) error {
	return c.mongo.DeleteUserInfoByName(ctx, username)
}

func (c *Client) ChangeUserPassword(ctx context.Context, username, hashedNewPassword string) error {
	return c.mongo.ChangeUserPassword(ctx, username, hashedNewPassword)
}

func (c *Client) FindUserInfoByID(ctx context.Context, id types.ID) (*database.UserInfo, error) {
	return c.mongo.FindUserInfoByID(ctx, id)
}

func (c *Client) FindUserInfoByName(ctx context.Context, username string) (*database.UserInfo, error) {
	return c.mongo.FindUserInfoByName(ctx, username)
}

func (c *Client) ListUserInfos(ctx context.Context) ([]*database.UserInfo, error) {
	return c.mongo.ListUserInfos(ctx)
}

func (c *Client) UpsertMemberInfo(
	ctx context.Context, projectID, userID, invitedBy types.ID, role database.MemberRole,
) (*database.MemberInfo, error) {
	return c.mongo.UpsertMemberInfo(ctx, projectID, userID, invitedBy, role)
}

func (c *Client) ListMemberInfos(ctx context.Context, projectID types.ID) ([]*database.MemberInfo, error) {
	return c.mongo.ListMemberInfos(ctx, projectID)
}

func (c *Client) FindMemberInfo(ctx context.Context, projectID, userID types.ID) (*database.MemberInfo, error) {
	return c.mongo.FindMemberInfo(ctx, projectID, userID)
}

func (c *Client) UpdateMemberRole(
	ctx context.Context, projectID, userID types.ID, role database.MemberRole,
) (*database.MemberInfo, error) {
	return c.mongo.UpdateMemberRole(ctx, projectID, userID, role)
}

func (c *Client) DeleteMemberInfo(ctx context.Context, projectID, userID types.ID) error {
	return c.mongo.DeleteMemberInfo(ctx, projectID, userID)
}

func (c *Client) CreateInviteInfo(
	ctx context.Context, projectID types.ID, token string, role database.MemberRole,
	createdBy types.ID, expiresAt *gotime.Time,
) (*database.InviteInfo, error) {
	return c.mongo.CreateInviteInfo(ctx, projectID, token, role, createdBy, expiresAt)
}

func (c *Client) FindInviteInfoByToken(ctx context.Context, token string) (*database.InviteInfo, error) {
	return c.mongo.FindInviteInfoByToken(ctx, token)
}

func (c *Client) DeleteExpiredInviteInfos(ctx context.Context, now gotime.Time) (int64, error) {
	return c.mongo.DeleteExpiredInviteInfos(ctx, now)
}

func (c *Client) ListProjectInfosByMember(ctx context.Context, userID types.ID) ([]*database.ProjectInfo, error) {
	return c.mongo.ListProjectInfosByMember(ctx, userID)
}

func (c *Client) FindCompactionCandidates(
	ctx context.Context,
	candidatesLimit, compactionMinChanges int,
	lastServerSeq int64,
	lastDocID types.ID,
) ([]*database.DocInfo, int64, types.ID, error) {
	return c.mongo.FindCompactionCandidates(
		ctx, candidatesLimit, compactionMinChanges, lastServerSeq, lastDocID,
	)
}

func (c *Client) FindDocInfoByKey(ctx context.Context, projectID types.ID, docKey key.Key) (*database.DocInfo, error) {
	return c.mongo.FindDocInfoByKey(ctx, projectID, docKey)
}

func (c *Client) FindDocInfosByKeys(
	ctx context.Context, projectID types.ID, docKeys []key.Key,
) ([]*database.DocInfo, error) {
	return c.mongo.FindDocInfosByKeys(ctx, projectID, docKeys)
}

func (c *Client) FindDocInfosByIDs(
	ctx context.Context, projectID types.ID, docIDs []types.ID,
) ([]*database.DocInfo, error) {
	return c.mongo.FindDocInfosByIDs(ctx, projectID, docIDs)
}

func (c *Client) FindOrCreateDocInfo(
	ctx context.Context, clientRefKey types.ClientRefKey, docKey key.Key,
) (*database.DocInfo, error) {
	return c.mongo.FindOrCreateDocInfo(ctx, clientRefKey, docKey)
}

func (c *Client) FindDocInfoByRefKey(ctx context.Context, refKey types.DocRefKey) (*database.DocInfo, error) {
	return c.mongo.FindDocInfoByRefKey(ctx, refKey)
}

func (c *Client) UpdateDocInfoStatusToRemoved(ctx context.Context, refKey types.DocRefKey) error {
	return c.mongo.UpdateDocInfoStatusToRemoved(ctx, refKey)
}

func (c *Client) UpdateDocInfoSchema(ctx context.Context, refKey types.DocRefKey, schemaKey string) error {
	return c.mongo.UpdateDocInfoSchema(ctx, refKey, schemaKey)
}

func (c *Client) GetProjectStatsCounts(
	ctx context.Context, projectID types.ID,
) (*database.ProjectStatsCounts, error) {
	return c.mongo.GetProjectStatsCounts(ctx, projectID)
}

func (c *Client) UpdateProjectStats(
	ctx context.Context,
	projectID types.ID,
	clientsCount int64,
	documentsCount int64,
	updatedAt gotime.Time,
) error {
	return c.mongo.UpdateProjectStats(ctx, projectID, clientsCount, documentsCount, updatedAt)
}

func (c *Client) CountAliveDocuments(ctx context.Context, projectID types.ID) (int64, error) {
	return c.mongo.CountAliveDocuments(ctx, projectID)
}

func (c *Client) FindProjectInfosForRefresh(
	ctx context.Context, limit int, lastID types.ID,
) ([]*database.ProjectInfo, types.ID, error) {
	return c.mongo.FindProjectInfosForRefresh(ctx, limit, lastID)
}

func (c *Client) FindDocInfosByPaging(
	ctx context.Context, projectID types.ID, paging types.Paging[types.ID],
) ([]*database.DocInfo, error) {
	return c.mongo.FindDocInfosByPaging(ctx, projectID, paging)
}

func (c *Client) FindDocInfosByQuery(
	ctx context.Context, projectID types.ID, query string, pageSize int,
) (*types.SearchResult[*database.DocInfo], error) {
	return c.mongo.FindDocInfosByQuery(ctx, projectID, query, pageSize)
}

func (c *Client) CreateSchemaInfo(
	ctx context.Context, projectID types.ID, name string, version int, body string, rules []types.Rule,
) (*database.SchemaInfo, error) {
	return c.mongo.CreateSchemaInfo(ctx, projectID, name, version, body, rules)
}

func (c *Client) GetSchemaInfo(
	ctx context.Context, projectID types.ID, name string, version int,
) (*database.SchemaInfo, error) {
	return c.mongo.GetSchemaInfo(ctx, projectID, name, version)
}

func (c *Client) GetSchemaInfos(
	ctx context.Context, projectID types.ID, name string,
) ([]*database.SchemaInfo, error) {
	return c.mongo.GetSchemaInfos(ctx, projectID, name)
}

func (c *Client) ListSchemaInfos(ctx context.Context, projectID types.ID) ([]*database.SchemaInfo, error) {
	return c.mongo.ListSchemaInfos(ctx, projectID)
}

func (c *Client) RemoveSchemaInfo(ctx context.Context, projectID types.ID, name string, version int) error {
	return c.mongo.RemoveSchemaInfo(ctx, projectID, name, version)
}

func (c *Client) IsSchemaAttached(ctx context.Context, projectID types.ID, schema string) (bool, error) {
	return c.mongo.IsSchemaAttached(ctx, projectID, schema)
}

func (c *Client) CreateRevisionInfo(
	ctx context.Context, docRefKey types.DocRefKey, label, description string, snapshot []byte,
) (*database.RevisionInfo, error) {
	return c.mongo.CreateRevisionInfo(ctx, docRefKey, label, description, snapshot)
}

func (c *Client) FindRevisionInfosByPaging(
	ctx context.Context, docRefKey types.DocRefKey, paging types.Paging[int], includeSnapshot bool,
) ([]*database.RevisionInfo, error) {
	return c.mongo.FindRevisionInfosByPaging(ctx, docRefKey, paging, includeSnapshot)
}

func (c *Client) FindRevisionInfoByID(ctx context.Context, revisionID types.ID) (*database.RevisionInfo, error) {
	return c.mongo.FindRevisionInfoByID(ctx, revisionID)
}

func (c *Client) DeleteRevisionInfo(ctx context.Context, revisionID types.ID) error {
	return c.mongo.DeleteRevisionInfo(ctx, revisionID)
}
