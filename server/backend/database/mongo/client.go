/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package mongo implements database interfaces using MongoDB.
package mongo

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	gotime "time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	// StatusKey is the key of the status field.
	StatusKey = "status"
)

// Client is a client that connects to Mongo DB and reads or saves Yorkie data.
type Client struct {
	config *Config
	client *mongo.Client

	cacheManager  *cache.Manager
	projectCache  *ProjectCache
	clientCache   *cache.LRU[types.ClientRefKey, *database.ClientInfo]
	docCache      *cache.LRU[types.DocRefKey, *database.DocInfo]
	changeCache   *cache.LRU[types.DocRefKey, *ChangeStore]
	presenceCache *cache.LRU[types.DocRefKey, *ChangeStore]
	vectorCache   *cache.LRU[types.DocRefKey, *cmap.Map[types.ID, time.VersionVector]]
}

// Dial creates an instance of Client and dials the given MongoDB.
func Dial(conf *Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), conf.ParseConnectionTimeout())
	defer cancel()

	clientOptions := options.Client().
		ApplyURI(conf.ConnectionURI).
		SetRegistry(NewRegistryBuilder())

	if conf.MonitoringEnabled {
		threshold, err := gotime.ParseDuration(conf.MonitoringSlowQueryThreshold)
		if err != nil {
			return nil, fmt.Errorf("parse slow query threshold duration: %w", err)
		}

		monitor := NewQueryMonitor(&MonitorConfig{
			Enabled:            conf.MonitoringEnabled,
			SlowQueryThreshold: threshold,
		})

		clientOptions.SetMonitor(monitor.CreateCommandMonitor())
	}

	client, err := mongo.Connect(
		clientOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("connect to MongoDB: %w", err)
	}

	pingTimeout := conf.ParsePingTimeout()
	ctxPing, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := client.Ping(ctxPing, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("ping MongoDB: %w", err)
	}

	if err := ensureIndexes(ctx, client.Database(conf.YorkieDatabase)); err != nil {
		return nil, err
	}

	cacheManager := cache.NewManager(conf.ParseCacheStatsInterval())

	projectCache, err := NewProjectCache(conf.ProjectCacheSize, conf.ParseProjectCacheTTL())
	if err != nil {
		return nil, fmt.Errorf("initialize project cache: %w", err)
	}
	cacheManager.RegisterCache(projectCache)

	clientCache, err := cache.NewLRU[types.ClientRefKey, *database.ClientInfo](conf.ClientCacheSize, "clients")
	if err != nil {
		return nil, fmt.Errorf("initialize client cache: %w", err)
	}
	cacheManager.RegisterCache(clientCache)

	docCache, err := cache.NewLRU[types.DocRefKey, *database.DocInfo](conf.DocCacheSize, "docs")
	if err != nil {
		return nil, fmt.Errorf("initialize document cache: %w", err)
	}
	cacheManager.RegisterCache(docCache)

	changeCache, err := cache.NewLRU[types.DocRefKey, *ChangeStore](conf.ChangeCacheSize, "changes")
	if err != nil {
		return nil, fmt.Errorf("initialize change cache: %w", err)
	}
	cacheManager.RegisterCache(changeCache)

	presenceCache, err := cache.NewLRU[types.DocRefKey, *ChangeStore](conf.ChangeCacheSize, "presences")
	if err != nil {
		return nil, fmt.Errorf("initialize presence cache: %w", err)
	}
	cacheManager.RegisterCache(presenceCache)

	vectorCache, err := cache.NewLRU[types.DocRefKey, *cmap.Map[types.ID, time.VersionVector]](
		conf.VectorCacheSize, "vectors",
	)
	if err != nil {
		return nil, fmt.Errorf("initialize version vector cache: %w", err)
	}
	cacheManager.RegisterCache(vectorCache)

	logging.DefaultLogger().Infof("MongoDB connected, URI: %s, DB: %s", conf.ConnectionURI, conf.YorkieDatabase)

	yorkieClient := &Client{
		config: conf,
		client: client,

		cacheManager:  cacheManager,
		projectCache:  projectCache,
		clientCache:   clientCache,
		docCache:      docCache,
		changeCache:   changeCache,
		presenceCache: presenceCache,
		vectorCache:   vectorCache,
	}

	if conf.CacheStatsEnabled {
		go cacheManager.StartPeriodicLogging(context.Background())
	}

	return yorkieClient, nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	if err := c.client.Disconnect(context.Background()); err != nil {
		return fmt.Errorf("close MongoDB client: %w", err)
	}

	c.cacheManager.Stop()

	c.projectCache.Purge()
	c.clientCache.Purge()
	c.docCache.Purge()
	c.changeCache.Purge()
	c.presenceCache.Purge()
	c.vectorCache.Purge()

	return nil
}

// InvalidateCache invalidates the cache of the given type and key.
func (c *Client) InvalidateCache(cacheType types.CacheType, key string) {
	switch cacheType {
	case types.CacheTypeProject:
		if id := types.ID(key); id.Validate() == nil {
			c.projectCache.Remove(id)
		}
	}
}

// TryLeadership attempts to acquire or renew leadership with the given lease duration.
// If leaseToken is empty, it attempts to acquire new leadership.
// If leaseToken is provided, it attempts to renew the existing lease.
func (c *Client) TryLeadership(
	ctx context.Context,
	rpcAddr,
	leaseToken string,
	leaseDuration gotime.Duration,
) (*database.ClusterNodeInfo, error) {
	leaseMS := leaseDuration.Milliseconds()

	if leaseToken == "" {
		ret, err := c.tryAcquireLeadership(ctx, rpcAddr, leaseMS)
		if err != nil {
			return nil, err
		}

		// If lease is nil, it means leadership acquisition failed.
		if ret == nil {
			if err = c.updateClusterFollower(ctx, rpcAddr); err != nil {
				return nil, err
			}
		}

		return ret, nil
	}

	return c.tryRenewLeadership(ctx, rpcAddr, leaseToken, leaseMS)
}

// tryAcquireLeadership attempts to acquire new leadership.
func (c *Client) tryAcquireLeadership(
	ctx context.Context,
	rpcAddr string,
	leaseMS int64,
) (*database.ClusterNodeInfo, error) {
	// Generate a new lease token
	token, err := database.GenerateLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("generate lease token: %w", err)
	}

	// Try to acquire leadership using atomic upsert.
	result := c.collection(ColClusterNodes).FindOneAndUpdate(
		ctx,
		bson.M{"rpc_addr": rpcAddr},
		mongo.Pipeline{
			{{Key: "$set", Value: bson.D{
				{Key: "expires_at", Value: bson.D{{Key: "$add", Value: bson.A{"$$NOW", leaseMS}}}},
				{Key: "lease_token", Value: token},
				{Key: "rpc_addr", Value: rpcAddr},
				{Key: "updated_at", Value: "$$NOW"},
				{Key: "is_leader", Value: true},
			}}},
		},
		options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After),
	)

	info := &database.ClusterNodeInfo{}
	if err := result.Decode(info); err != nil {
		// If the error is due to a duplicate key, it means another node has
		// already acquired leadership.
		if mongo.IsDuplicateKeyError(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("decode new cluster node: %w", err)
	}

	// Successfully acquired leadership
	return info, nil
}

// tryRenewLeadership attempts to renew existing leadership
func (c *Client) tryRenewLeadership(
	ctx context.Context,
	rpcAddr string,
	leaseToken string,
	leaseMS int64,
) (*database.ClusterNodeInfo, error) {
	// Generate a new lease token for renewal
	newLeaseToken, err := database.GenerateLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("renew leadership of %s: %w", rpcAddr, err)
	}

	// Try to update the existing leadership with the correct token and rpcAddr.
	result := c.collection(ColClusterNodes).FindOneAndUpdate(
		ctx,
		bson.M{
			"rpc_addr":    rpcAddr,
			"is_leader":   true,
			"lease_token": leaseToken,
			"$expr":       bson.M{"$gte": bson.A{"$expires_at", "$$NOW"}},
		},
		mongo.Pipeline{
			{{Key: "$set", Value: bson.D{
				{Key: "lease_token", Value: newLeaseToken},
				{Key: "expires_at", Value: bson.D{{Key: "$add", Value: bson.A{"$$NOW", leaseMS}}}},
				{Key: "updated_at", Value: "$$NOW"},
			}}},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("renew leadership of %s: %w", rpcAddr, database.ErrInvalidLeaseToken)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("renew leadership of %s: %w", rpcAddr, result.Err())
	}

	info := &database.ClusterNodeInfo{}
	if err := result.Decode(info); err != nil {
		return nil, fmt.Errorf("renew leadership of %s: %w", rpcAddr, err)
	}

	return info, nil
}

// updateClusterFollower updates the given node as follower.
func (c *Client) updateClusterFollower(ctx context.Context, rpcAddr string) error {
	_, err := c.collection(ColClusterNodes).UpdateOne(
		ctx,
		bson.M{"rpc_addr": rpcAddr},
		bson.M{
			"$currentDate": bson.M{"updated_at": true},
			"$setOnInsert": bson.M{
				"rpc_addr":  rpcAddr,
				"is_leader": false,
			},
		},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("update cluster follower of %s: %w", rpcAddr, err)
	}

	return nil
}

// FindClusterNodes returns all cluster nodes that have been updated within the given time window.
// Results are sorted with the leader first, then by updated_at descending.
func (c *Client) FindClusterNodes(
	ctx context.Context,
	window gotime.Duration,
) ([]*database.ClusterNodeInfo, error) {
	cursor, err := c.collection(ColClusterNodes).Find(
		ctx,
		bson.M{"$expr": bson.M{"$gte": bson.A{
			"$updated_at", bson.D{{Key: "$add", Value: bson.A{"$$NOW", -window.Milliseconds()}}},
		}}},
		options.Find().SetSort(bson.D{
			{Key: "is_leader", Value: -1},
			{Key: "updated_at", Value: -1},
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("find cluster nodes: %w", err)
	}

	var nodes []*database.ClusterNodeInfo
	if err = cursor.All(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("find cluster nodes: %w", err)
	}

	return nodes, nil
}

// RemoveClusterNode removes the cluster node with the given rpcAddr.
func (c *Client) RemoveClusterNode(ctx context.Context, rpcAddr string) error {
	_, err := c.collection(ColClusterNodes).DeleteOne(ctx, bson.M{"rpc_addr": rpcAddr})
	if err != nil {
		return fmt.Errorf("remove cluster node with %s: %w", rpcAddr, err)
	}
	return nil
}

// RemoveClusterNodes removes all cluster nodes.
func (c *Client) RemoveClusterNodes(ctx context.Context) error {
	_, err := c.collection(ColClusterNodes).DeleteMany(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("remove cluster nodes: %w", err)
	}
	return nil
}

// EnsureDefaultUserAndProject creates the default user and project if they do not exist.
func (c *Client) EnsureDefaultUserAndProject(
	ctx context.Context,
	username,
	password string,
) (*database.UserInfo, *database.ProjectInfo, error) {
	userInfo, err := c.ensureDefaultUserInfo(ctx, username, password)
	if err != nil {
		return nil, nil, err
	}

	projectInfo, err := c.ensureDefaultProjectInfo(ctx, userInfo.ID)
	if err != nil {
		return nil, nil, err
	}

	return userInfo, projectInfo, nil
}

// ensureDefaultUserInfo creates the default user info if it does not exist.
func (c *Client) ensureDefaultUserInfo(
	ctx context.Context,
	username,
	password string,
) (*database.UserInfo, error) {
	hashedPassword, err := database.HashedPassword(password)
	if err != nil {
		return nil, err
	}

	candidate := database.NewUserInfo(
		username,
		hashedPassword,
	)

	_, err = c.collection(ColUsers).UpdateOne(ctx, bson.M{
		"username": candidate.Username,
	}, bson.M{
		"$setOnInsert": bson.M{
			"username":        candidate.Username,
			"hashed_password": candidate.HashedPassword,
			"created_at":      candidate.CreatedAt,
		},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("upsert default user: %w", err)
	}

	result := c.collection(ColUsers).FindOne(ctx, bson.M{
		"username": candidate.Username,
	})

	info := &database.UserInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("default: %w", database.ErrUserNotFound)
		}

		return nil, fmt.Errorf("decode user %s: %w", username, err)
	}

	return info, nil
}

// ensureDefaultProjectInfo creates the default project info if it does not exist.
func (c *Client) ensureDefaultProjectInfo(
	ctx context.Context,
	defaultUserID types.ID,
) (*database.ProjectInfo, error) {
	candidate := database.NewProjectInfo(database.DefaultProjectName, defaultUserID)
	candidate.ID = database.DefaultProjectID

	_, err := c.collection(ColProjects).UpdateOne(ctx, bson.M{
		"_id": candidate.ID,
	}, bson.M{
		"$setOnInsert": bson.M{
			"name":                            candidate.Name,
			"owner":                           candidate.Owner,
			"auth_webhook_max_retries":        candidate.AuthWebhookMaxRetries,
			"auth_webhook_min_wait_interval":  candidate.AuthWebhookMinWaitInterval,
			"auth_webhook_max_wait_interval":  candidate.AuthWebhookMaxWaitInterval,
			"auth_webhook_request_timeout":    candidate.AuthWebhookRequestTimeout,
			"event_webhook_max_retries":       candidate.EventWebhookMaxRetries,
			"event_webhook_min_wait_interval": candidate.EventWebhookMinWaitInterval,
			"event_webhook_max_wait_interval": candidate.EventWebhookMaxWaitInterval,
			"event_webhook_request_timeout":   candidate.EventWebhookRequestTimeout,
			"client_deactivate_threshold":     candidate.ClientDeactivateThreshold,
			"snapshot_threshold":              candidate.SnapshotThreshold,
			"snapshot_interval":               candidate.SnapshotInterval,
			"auto_revision_enabled":           candidate.AutoRevisionEnabled,
			"max_subscribers_per_document":    candidate.MaxSubscribersPerDocument,
			"max_attachments_per_document":    candidate.MaxAttachmentsPerDocument,
			"max_size_per_document":           candidate.MaxSizePerDocument,
			"remove_on_detach":                candidate.RemoveOnDetach,
			"public_key":                      candidate.PublicKey,
			"secret_key":                      candidate.SecretKey,
			"created_at":                      candidate.CreatedAt,
		},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("create default project: %w", err)
	}

	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"_id": candidate.ID,
	})

	info := &database.ProjectInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("default: %w", database.ErrProjectNotFound)
		}

		return nil, fmt.Errorf("decode project: %w", err)
	}

	return info, nil
}

// CreateProjectInfo creates a new project.
func (c *Client) CreateProjectInfo(
	ctx context.Context,
	name string,
	owner types.ID,
) (*database.ProjectInfo, error) {
	info := database.NewProjectInfo(name, owner)
	result, err := c.collection(ColProjects).InsertOne(ctx, bson.M{
		"name":                            info.Name,
		"owner":                           owner,
		"auth_webhook_max_retries":        info.AuthWebhookMaxRetries,
		"auth_webhook_min_wait_interval":  info.AuthWebhookMinWaitInterval,
		"auth_webhook_max_wait_interval":  info.AuthWebhookMaxWaitInterval,
		"auth_webhook_request_timeout":    info.AuthWebhookRequestTimeout,
		"event_webhook_max_retries":       info.EventWebhookMaxRetries,
		"event_webhook_min_wait_interval": info.EventWebhookMinWaitInterval,
		"event_webhook_max_wait_interval": info.EventWebhookMaxWaitInterval,
		"event_webhook_request_timeout":   info.EventWebhookRequestTimeout,
		"client_deactivate_threshold":     info.ClientDeactivateThreshold,
		"snapshot_threshold":              info.SnapshotThreshold,
		"snapshot_interval":               info.SnapshotInterval,
		"max_subscribers_per_document":    info.MaxSubscribersPerDocument,
		"max_attachments_per_document":    info.MaxAttachmentsPerDocument,
		"max_size_per_document":           info.MaxSizePerDocument,
		"remove_on_detach":                info.RemoveOnDetach,
		"public_key":                      info.PublicKey,
		"secret_key":                      info.SecretKey,
		"created_at":                      info.CreatedAt,
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, database.ErrProjectAlreadyExists
		}

		return nil, fmt.Errorf("create project %s: %w", name, err)
	}

	info.ID = types.ID(result.InsertedID.(bson.ObjectID).Hex())
	return info, nil
}

// ListProjectInfos returns all project infos owned by owner.
func (c *Client) ListProjectInfos(
	ctx context.Context,
	owner types.ID,
) ([]*database.ProjectInfo, error) {
	cursor, err := c.collection(ColProjects).Find(ctx, bson.M{
		"owner": owner,
	})
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	var infos []*database.ProjectInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}

	return infos, nil
}

// FindProjectInfoByPublicKey returns a project by public key.
func (c *Client) FindProjectInfoByPublicKey(ctx context.Context, publicKey string) (*database.ProjectInfo, error) {
	if cached, ok := c.projectCache.GetByAPIKey(publicKey); ok {
		return cached.DeepCopy(), nil
	}

	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"public_key": publicKey,
	})

	info := &database.ProjectInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", publicKey, database.ErrProjectNotFound)
		}

		return nil, fmt.Errorf("find project by public key %s: %w", publicKey, err)
	}

	c.projectCache.Add(info)

	return info, nil
}

// FindProjectInfoBySecretKey returns a project by secret key.
func (c *Client) FindProjectInfoBySecretKey(ctx context.Context, secretKey string) (*database.ProjectInfo, error) {
	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"secret_key": secretKey,
	})

	info := &database.ProjectInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", secretKey, database.ErrProjectNotFound)
		}

		return nil, fmt.Errorf("find project by secret key: %w", err)
	}

	return info, nil
}

// FindProjectInfoByName returns a project by name.
func (c *Client) FindProjectInfoByName(
	ctx context.Context,
	owner types.ID,
	name string,
) (*database.ProjectInfo, error) {
	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"name":  name,
		"owner": owner,
	})

	info := &database.ProjectInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", name, database.ErrProjectNotFound)
		}

		return nil, fmt.Errorf("find project by name %s: %w", name, err)
	}

	return info, nil
}

// FindProjectInfoByID returns a project by the given id.
func (c *Client) FindProjectInfoByID(ctx context.Context, id types.ID) (*database.ProjectInfo, error) {
	if cached, ok := c.projectCache.GetByID(id); ok {
		return cached.DeepCopy(), nil
	}

	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"_id": id,
	})

	info := &database.ProjectInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("find project by id %s: %w", id, err)
	}

	c.projectCache.Add(info)

	return info, nil
}

// UpdateProjectInfo updates the project info.
func (c *Client) UpdateProjectInfo(
	ctx context.Context,
	owner types.ID,
	id types.ID,
	fields *types.UpdatableProjectFields,
) (*database.ProjectInfo, error) {
	// Convert UpdatableProjectFields to bson.M
	updatableFields := bson.M{}
	data, err := bson.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal fields: %w", err)
	}
	if err = bson.Unmarshal(data, &updatableFields); err != nil {
		return nil, fmt.Errorf("unmarshal updatable fields: %w", err)
	}
	updatableFields["updated_at"] = gotime.Now()

	res := c.collection(ColProjects).FindOneAndUpdate(ctx, bson.M{
		"_id":   id,
		"owner": owner,
	}, bson.M{
		"$set": updatableFields,
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	info := &database.ProjectInfo{}
	if err := res.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		if mongo.IsDuplicateKeyError(err) {
			return nil, fmt.Errorf("%s: %w", *fields.Name, database.ErrProjectAlreadyExists)
		}
		return nil, fmt.Errorf("decode project: %w", err)
	}

	c.projectCache.Remove(info.ID)

	return info, nil
}

// RotateProjectKeys rotates the API keys of the project.
func (c *Client) RotateProjectKeys(
	ctx context.Context,
	owner types.ID,
	id types.ID,
	publicKey string,
	secretKey string,
) (*database.ProjectInfo, *database.ProjectInfo, error) {

	prevInfo := &database.ProjectInfo{}
	res := c.collection(ColProjects).FindOne(ctx, bson.M{"_id": id, "owner": owner})
	if err := res.Decode(prevInfo); err == nil {
		c.projectCache.Remove(prevInfo.ID)
	}

	res = c.collection(ColProjects).FindOneAndUpdate(ctx, bson.M{
		"_id":   id,
		"owner": owner,
	}, bson.M{
		"$set": bson.M{
			"public_key": publicKey,
			"secret_key": secretKey,
			"updated_at": gotime.Now(),
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	// Handle errors and decode result
	info := &database.ProjectInfo{}
	if err := res.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		return nil, nil, fmt.Errorf("rotate project keys of %s: %w", id, err)
	}

	return info, prevInfo, nil
}

// CreateUserInfo creates a new user.
func (c *Client) CreateUserInfo(
	ctx context.Context,
	username string,
	hashedPassword string,
) (*database.UserInfo, error) {
	info := database.NewUserInfo(username, hashedPassword)
	result, err := c.collection(ColUsers).InsertOne(ctx, bson.M{
		"username":        info.Username,
		"hashed_password": info.HashedPassword,
		"created_at":      info.CreatedAt,
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, database.ErrUserAlreadyExists
		}

		return nil, fmt.Errorf("create user %s: %w", username, err)
	}

	info.ID = types.ID(result.InsertedID.(bson.ObjectID).Hex())
	return info, nil
}

// GetOrCreateUserInfoByGitHubID returns a user by the given GitHub ID.
func (c *Client) GetOrCreateUserInfoByGitHubID(
	ctx context.Context,
	githubID string,
) (*database.UserInfo, error) {
	now := gotime.Now()
	result := c.collection(ColUsers).FindOneAndUpdate(
		ctx,
		bson.M{
			"username": githubID,
		},
		bson.M{
			"$set": bson.M{
				"accessed_at": now,
			},
			"$setOnInsert": bson.M{
				"auth_provider": "github",
				"created_at":    now,
			},
		},
		options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After),
	)

	info := &database.UserInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", githubID, database.ErrUserNotFound)
		}

		return nil, fmt.Errorf("create user %s: %w", githubID, err)
	}

	return info, nil
}

// DeleteUserInfoByName deletes a user by name.
func (c *Client) DeleteUserInfoByName(ctx context.Context, username string) error {
	deleteResult, err := c.collection(ColUsers).DeleteOne(ctx, bson.M{
		"username": username,
	})
	if err != nil {
		return err
	}
	if deleteResult.DeletedCount == 0 {
		return fmt.Errorf("delete user %s: %w", username, database.ErrUserNotFound)
	}
	return nil
}

// ChangeUserPassword changes to new password for user.
func (c *Client) ChangeUserPassword(ctx context.Context, username, hashedNewPassword string) error {
	updateResult, err := c.collection(ColUsers).UpdateOne(ctx,
		bson.M{"username": username},
		bson.M{"$set": bson.M{"hashed_password": hashedNewPassword}},
	)
	if err != nil {
		return err
	}
	if updateResult.ModifiedCount == 0 {
		return fmt.Errorf("change password of %s: %w", username, database.ErrUserNotFound)
	}
	return nil
}

// FindUserInfoByID returns a user by ID.
func (c *Client) FindUserInfoByID(ctx context.Context, userID types.ID) (*database.UserInfo, error) {
	result := c.collection(ColUsers).FindOne(ctx, bson.M{
		"_id": userID,
	})

	info := &database.UserInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("find user %s: %w", userID, database.ErrUserNotFound)
		}

		return nil, fmt.Errorf("find user %s: %w", userID, err)
	}

	return info, nil
}

// FindUserInfoByName returns a user by username.
func (c *Client) FindUserInfoByName(ctx context.Context, username string) (*database.UserInfo, error) {
	result := c.collection(ColUsers).FindOne(ctx, bson.M{
		"username": username,
	})

	userInfo := &database.UserInfo{}
	if err := result.Decode(userInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("find user %s: %w", username, database.ErrUserNotFound)
		}
		return nil, fmt.Errorf("find user %s: %w", username, err)
	}

	return userInfo, nil
}

// ListUserInfos returns all user infos.
func (c *Client) ListUserInfos(
	ctx context.Context,
) ([]*database.UserInfo, error) {
	cursor, err := c.collection(ColUsers).Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	var infos []*database.UserInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	return infos, nil
}

// ActivateClient activates the client of the given key.
func (c *Client) ActivateClient(
	ctx context.Context,
	projectID types.ID,
	key string,
	metadata map[string]string,
) (*database.ClientInfo, error) {
	now := gotime.Now()

	info := &database.ClientInfo{
		ID:        types.NewID(),
		ProjectID: projectID,
		Key:       key,
		Status:    database.ClientActivated,
		Documents: make(database.ClientDocInfoMap),
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := c.collection(ColClients).InsertOne(ctx, info); err != nil {
		return nil, fmt.Errorf("insert client: %w", err)
	}

	refKey := types.ClientRefKey{ProjectID: projectID, ClientID: info.ID}
	c.clientCache.Add(refKey, info.DeepCopy())

	return info, nil
}

// TryAttaching updates the status of the document to Attaching to prevent
// deactivating the client while the document is being attached.
func (c *Client) TryAttaching(
	ctx context.Context,
	refKey types.ClientRefKey,
	docID types.ID,
) (*database.ClientInfo, error) {
	// client must be activated and document must not be attached
	result := c.collection(ColClients).FindOneAndUpdate(
		ctx,
		bson.M{
			"project_id":                       refKey.ProjectID,
			"_id":                              refKey.ClientID,
			"status":                           database.ClientActivated,
			clientDocInfoKey(docID, StatusKey): bson.M{"$ne": database.DocumentAttached},
		},
		bson.M{
			"$set": bson.M{
				clientDocInfoKey(docID, StatusKey):    database.DocumentAttaching,
				clientDocInfoKey(docID, "server_seq"): int64(0),
				clientDocInfoKey(docID, "client_seq"): uint32(0),
				"updated_at":                          gotime.Now(),
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	info := &database.ClientInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, database.ErrClientNotFound)
		}

		return nil, fmt.Errorf("try attaching %s to %s : %w", docID, refKey.ClientID, err)
	}

	c.clientCache.Add(refKey, info.DeepCopy())

	return info, nil
}

// DeactivateClient deactivates the client of the given refKey.
func (c *Client) DeactivateClient(
	ctx context.Context,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	now := gotime.Now()

	result := c.collection(ColClients).FindOneAndUpdate(
		ctx,
		bson.M{
			"project_id": refKey.ProjectID,
			"_id":        refKey.ClientID,
			"status":     database.ClientActivated,
			// Ensure that no documents are currently attaching or attached
			"$expr": bson.M{"$not": bson.M{"$anyElementTrue": bson.M{"$map": bson.M{
				"input": bson.M{"$ifNull": bson.A{bson.M{"$objectToArray": "$documents"}, bson.A{}}},
				"as":    "doc",
				"in": bson.M{"$in": bson.A{
					"$$doc.v.status", bson.A{database.DocumentAttaching, database.DocumentAttached}},
				},
			}}}},
		},
		bson.M{
			"$set": bson.M{
				"status":     database.ClientDeactivated,
				"updated_at": now,
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	info := &database.ClientInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, err)
	}

	c.clientCache.Add(refKey, info.DeepCopy())

	return info, nil
}

// FindClientInfoByRefKey finds the client of the given refKey.
func (c *Client) FindClientInfoByRefKey(
	ctx context.Context,
	refKey types.ClientRefKey,
	skipCache ...bool,
) (*database.ClientInfo, error) {
	skip := len(skipCache) > 0 && skipCache[0]

	if !skip {
		if cached, ok := c.clientCache.Get(refKey); ok {
			return cached.DeepCopy(), nil
		}
	}

	result := c.collection(ColClients).FindOne(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.ClientID,
	})

	info := &database.ClientInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("find client of %s: %w", refKey, database.ErrClientNotFound)
		}

		return nil, fmt.Errorf("find client of %s: %w", refKey, err)
	}

	if !skip {
		c.clientCache.Add(refKey, info.DeepCopy())
	}

	return info, nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (c *Client) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	info *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	clientKey := types.ClientRefKey{ProjectID: info.ProjectID, ClientID: info.ID}
	clientDocInfo, ok := info.Documents[docInfo.ID]
	if !ok {
		return fmt.Errorf(
			"update client of %s after PP %s: %w",
			info.ID, docInfo.ID, database.ErrDocumentNeverAttached,
		)
	}

	if existing, ok := c.clientCache.Get(clientKey); ok {
		if existingDocInfo, ok := existing.Documents[docInfo.ID]; ok {
			if existingDocInfo.ServerSeq >= clientDocInfo.ServerSeq &&
				existingDocInfo.ClientSeq >= clientDocInfo.ClientSeq &&
				existingDocInfo.Status == clientDocInfo.Status {
				return nil
			}
		}
	}

	c.clientCache.Remove(clientKey)

	attached, err := info.IsAttached(docInfo.ID)
	if err != nil {
		return err
	}

	// NOTE(hackerwins): Clear presence changes of the given client on the
	// document if the client is no longer attached to the document.
	docKey := types.DocRefKey{ProjectID: info.ProjectID, DocID: docInfo.ID}
	if prStore, ok := c.presenceCache.Get(docKey); ok && !attached {
		prStore.RemoveChangesByActor(info.ID)
	}

	var updater bson.M
	if attached {
		updater = bson.M{
			"$max": bson.M{
				clientDocInfoKey(docInfo.ID, "server_seq"): clientDocInfo.ServerSeq,
				clientDocInfoKey(docInfo.ID, "client_seq"): clientDocInfo.ClientSeq,
			},
			"$set": bson.M{
				clientDocInfoKey(docInfo.ID, StatusKey): clientDocInfo.Status,
				"updated_at":                            info.UpdatedAt,
			},
			"$addToSet": bson.M{
				"attached_docs": docInfo.ID,
			},
		}
	} else {
		updater = bson.M{
			"$set": bson.M{
				clientDocInfoKey(docInfo.ID, "server_seq"): 0,
				clientDocInfoKey(docInfo.ID, "client_seq"): 0,
				clientDocInfoKey(docInfo.ID, StatusKey):    clientDocInfo.Status,
				"updated_at":                               info.UpdatedAt,
			},
			"$pull": bson.M{
				"attached_docs": docInfo.ID,
			},
		}
	}

	result := c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
		"project_id": info.ProjectID,
		"_id":        info.ID,
	}, updater, options.FindOneAndUpdate().SetReturnDocument(options.After))

	updated := &database.ClientInfo{}
	if err := result.Decode(updated); err != nil {
		if err == mongo.ErrNoDocuments {
			return fmt.Errorf("decode client of %s after PP %s: %w", info.ID, docInfo.ID, database.ErrClientNotFound)
		}

		return fmt.Errorf("decode client info %s after PP %s: %w", info.ID, docInfo.ID, err)
	}

	c.clientCache.Add(clientKey, updated.DeepCopy())

	return nil
}

// FindAttachedClientInfosByRefKey returns the attached client infos of the given document.
func (c *Client) FindAttachedClientInfosByRefKey(
	ctx context.Context,
	docRefKey types.DocRefKey,
) ([]*database.ClientInfo, error) {
	filter := bson.M{
		"project_id":    docRefKey.ProjectID,
		"status":        database.ClientActivated,
		"attached_docs": docRefKey.DocID,
	}

	cursor, err := c.collection(ColClients).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("find attached clients of %s: %w", docRefKey, err)
	}

	var infos []*database.ClientInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("find attached clients of %s: %w", docRefKey, err)
	}

	for _, info := range infos {
		refKey := types.ClientRefKey{ProjectID: info.ProjectID, ClientID: info.ID}
		c.clientCache.Add(refKey, info.DeepCopy())
	}

	return infos, nil
}

// FindActiveClients finds active clients for deactivation checking.
func (c *Client) FindActiveClients(
	ctx context.Context,
	candidatesLimit int,
	lastClientID types.ID,
) ([]*database.ClientInfo, types.ID, error) {
	cursor, err := c.collection(ColClients).Find(ctx, bson.M{
		"_id":    bson.M{"$gt": lastClientID},
		"status": database.ClientActivated,
	}, options.Find().SetSort(bson.M{"_id": 1}).SetLimit(int64(candidatesLimit)))
	if err != nil {
		return nil, database.ZeroID, fmt.Errorf("find active clients: %w", err)
	}

	var infos []*database.ClientInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, database.ZeroID, fmt.Errorf("fetch active clients: %w", err)
	}

	var lastID types.ID = database.ZeroID
	if len(infos) > 0 {
		lastID = infos[len(infos)-1].ID
	}

	return infos, lastID, nil
}

// FindCompactionCandidates finds documents that need compaction.
func (c *Client) FindCompactionCandidates(
	ctx context.Context,
	candidatesLimit int,
	compactionMinChanges int,
	lastDocID types.ID,
) ([]*database.DocInfo, types.ID, error) {
	cursor, err := c.collection(ColDocuments).Find(ctx, bson.M{
		"_id":        bson.M{"$gt": lastDocID},
		"server_seq": bson.M{"$gte": compactionMinChanges},
	}, options.Find().SetSort(bson.M{"_id": 1}).SetLimit(int64(candidatesLimit)))
	if err != nil {
		return nil, database.ZeroID, fmt.Errorf("find compaction candidates direct: %w", err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, database.ZeroID, fmt.Errorf("fetch compaction candidates direct: %w", err)
	}

	var lastID types.ID = database.ZeroID
	if len(infos) > 0 {
		lastID = infos[len(infos)-1].ID
	}

	return infos, lastID, nil
}

// FindAttachedClientCountsByDocIDs returns the number of attached clients of the given documents as a map.
func (c *Client) FindAttachedClientCountsByDocIDs(
	ctx context.Context,
	projectID types.ID,
	docIDs []types.ID,
) (map[types.ID]int, error) {
	if len(docIDs) == 0 {
		return map[types.ID]int{}, nil
	}
	cursor, err := c.collection(ColClients).Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{
			"project_id": projectID,
			"status":     database.ClientActivated,
			"attached_docs": bson.M{
				"$in": docIDs,
			},
		}}},
		bson.D{{Key: "$unwind", Value: "$attached_docs"}},
		bson.D{{Key: "$match", Value: bson.M{"attached_docs": bson.M{"$in": docIDs}}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   "$attached_docs",
			"count": bson.M{"$sum": 1},
		}}},
	})
	if err != nil {
		return nil, fmt.Errorf("find attached client counts of %s: %w", docIDs, err)
	}

	defer func() {
		_ = cursor.Close(ctx)
	}()

	var attachedClientMap = make(map[types.ID]int)
	for _, docID := range docIDs {
		attachedClientMap[docID] = 0
	}

	for cursor.Next(ctx) {
		var attachedInfo struct {
			ID    types.ID `bson:"_id"`
			Count int      `bson:"count"`
		}
		if err := cursor.Decode(&attachedInfo); err != nil {
			return nil, fmt.Errorf("find attached client counts of %s: %w", docIDs, err)
		}
		attachedClientMap[attachedInfo.ID] = attachedInfo.Count
	}

	return attachedClientMap, nil
}

// FindOrCreateDocInfo finds the document or creates it if it does not exist.
func (c *Client) FindOrCreateDocInfo(
	ctx context.Context,
	clientRefKey types.ClientRefKey,
	docKey key.Key,
) (*database.DocInfo, error) {
	filter := bson.M{
		"project_id": clientRefKey.ProjectID,
		"key":        docKey,
		"removed_at": bson.M{
			"$exists": false,
		},
	}

	now := gotime.Now()
	result := c.collection(ColDocuments).FindOneAndUpdate(
		ctx,
		filter,
		bson.M{
			"$set": bson.M{
				"accessed_at": now,
			},
			"$setOnInsert": bson.M{
				"owner":      clientRefKey.ClientID,
				"server_seq": 0,
				"created_at": now,
				"updated_at": now,
			},
		},
		options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After),
	)

	if result.Err() != nil && mongo.IsDuplicateKeyError(result.Err()) {
		// NOTE(hackerwins): If duplicate key error occurred, retry with a
		// simple find operation since another concurrent request successfully
		// created the document.
		result = c.collection(ColDocuments).FindOne(ctx, filter)
	}

	info := &database.DocInfo{}
	if err := result.Decode(info); err != nil {
		return nil, fmt.Errorf("find or create document of %s: %w", docKey, err)
	}

	return info, nil
}

// FindDocInfoByKey finds the document of the given key.
func (c *Client) FindDocInfoByKey(
	ctx context.Context,
	projectID types.ID,
	docKey key.Key,
) (*database.DocInfo, error) {
	result := c.collection(ColDocuments).FindOne(ctx, bson.M{
		"project_id": projectID,
		"key":        docKey,
		"removed_at": bson.M{
			"$exists": false,
		},
	})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("%s %s: %w", projectID, docKey, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find document of %s: %w", docKey, result.Err())
	}

	info := &database.DocInfo{}
	if err := result.Decode(info); err != nil {
		return nil, fmt.Errorf("find document of %s: %w", docKey, err)
	}

	return info, nil
}

// FindDocInfosByKeys finds the documents of the given keys.
func (c *Client) FindDocInfosByKeys(
	ctx context.Context,
	projectID types.ID,
	docKeys []key.Key,
) ([]*database.DocInfo, error) {
	if len(docKeys) == 0 {
		return nil, nil
	}
	filter := bson.M{
		"project_id": projectID,
		"key": bson.M{
			"$in": docKeys,
		},
		"removed_at": bson.M{
			"$exists": false,
		},
	}

	cursor, err := c.collection(ColDocuments).Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("find documents of %v: %w", docKeys, err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("find documents of %v: %w", docKeys, err)
	}

	return infos, nil
}

// FindDocInfosByIDs finds the documents of the given ids.
func (c *Client) FindDocInfosByIDs(
	ctx context.Context,
	projectID types.ID,
	docIDs []types.ID,
) ([]*database.DocInfo, error) {
	if len(docIDs) == 0 {
		return nil, nil
	}

	cursor, err := c.collection(ColDocuments).Find(ctx, bson.M{
		"project_id": projectID,
		"_id":        bson.M{"$in": docIDs},
	})
	if err != nil {
		return nil, fmt.Errorf("find documents of %v: %w", docIDs, err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("find documents of %v: %w", docIDs, err)
	}

	return infos, nil
}

// FindDocInfoByRefKey finds a docInfo of the given refKey.
func (c *Client) FindDocInfoByRefKey(
	ctx context.Context,
	refKey types.DocRefKey,
) (*database.DocInfo, error) {
	if info, ok := c.docCache.Get(refKey); ok {
		return info.DeepCopy(), nil
	}

	result := c.collection(ColDocuments).FindOne(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.DocID,
	})

	info := &database.DocInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", refKey, database.ErrDocumentNotFound)
		}

		return nil, fmt.Errorf("find document of %s: %w", refKey, err)
	}

	c.docCache.Add(refKey, info.DeepCopy())

	return info, nil
}

// UpdateDocInfoStatusToRemoved updates the document status to removed.
func (c *Client) UpdateDocInfoStatusToRemoved(
	ctx context.Context,
	refKey types.DocRefKey,
) error {
	c.docCache.Remove(refKey)
	result := c.collection(ColDocuments).FindOneAndUpdate(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.DocID,
	}, bson.M{
		"$set": bson.M{
			"removed_at": gotime.Now(),
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	if result.Err() == mongo.ErrNoDocuments {
		return fmt.Errorf("update %s to removed: %w", refKey, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return fmt.Errorf("update %s to removed: %w", refKey, result.Err())
	}

	return nil
}

// UpdateDocInfoSchema updates the document schema.
func (c *Client) UpdateDocInfoSchema(
	ctx context.Context,
	refKey types.DocRefKey,
	schemaKey string,
) error {
	result := c.collection(ColDocuments).FindOneAndUpdate(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.DocID,
	}, bson.M{
		"$set": bson.M{
			"schema": schemaKey,
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	if result.Err() == mongo.ErrNoDocuments {
		return fmt.Errorf("update schema %s of %s: %w", schemaKey, refKey, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return fmt.Errorf("update schema %s of %s: %w", schemaKey, refKey, result.Err())
	}

	return nil
}

// GetDocumentsCount returns the number of documents in the given project.
func (c *Client) GetDocumentsCount(
	ctx context.Context,
	projectID types.ID,
) (int64, error) {
	count, err := c.collection(ColDocuments).CountDocuments(ctx, bson.M{
		"project_id": projectID,
		"removed_at": bson.M{
			"$exists": false,
		},
	})
	if err != nil {
		return 0, fmt.Errorf("count documents of %s: %w", projectID, err)
	}

	return count, nil
}

// GetClientsCount returns the number of active clients in the given project.
func (c *Client) GetClientsCount(ctx context.Context, projectID types.ID) (int64, error) {
	count, err := c.collection(ColClients).CountDocuments(ctx, bson.M{
		"project_id": projectID,
		StatusKey:    database.ClientActivated,
	})
	if err != nil {
		return 0, fmt.Errorf("count clients of %s: %w", projectID, err)
	}

	return count, nil
}

// CreateChangeInfos stores the given changes and doc info.
func (c *Client) CreateChangeInfos(
	ctx context.Context,
	refKey types.DocRefKey,
	checkpoint change.Checkpoint,
	changes []*database.ChangeInfo,
	isRemoved bool,
) (*database.DocInfo, change.Checkpoint, error) {
	// 01. Fetch the document info.
	docInfo, err := c.FindDocInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, change.InitialCheckpoint, err
	}
	if len(changes) == 0 && !isRemoved {
		return docInfo, checkpoint, nil
	}

	initialServerSeq := docInfo.ServerSeq
	now := gotime.Now()

	// 02. Separate presence-only changes and operation changes.
	hasOperations := false
	var prChanges []*database.ChangeInfo
	var opChanges []*database.ChangeInfo
	for _, cn := range changes {
		serverSeq := docInfo.IncreaseServerSeq()
		checkpoint = checkpoint.NextServerSeq(serverSeq)
		cn.ServerSeq = serverSeq
		checkpoint = checkpoint.SyncClientSeq(cn.ClientSeq)

		if cn.PresenceOnly() {
			prChanges = append(prChanges, cn)
			continue
		}

		if cn.HasOperations() {
			opChanges = append(opChanges, cn)
			hasOperations = true
		}
	}

	// 03. Store presence-only changes.
	if len(prChanges) > 0 {
		var prStore *ChangeStore
		if cached, ok := c.presenceCache.Get(refKey); ok {
			prStore = cached
		} else {
			prStore = NewChangeStore()
			c.presenceCache.Add(refKey, prStore)
		}
		prStore.ReplaceOrInsert(prChanges)
	}

	// 04. Store operation changes.
	if len(opChanges) > 0 {
		var opModels []mongo.WriteModel
		for _, c := range opChanges {
			opModels = append(opModels, mongo.NewUpdateOneModel().SetFilter(bson.M{
				"project_id": refKey.ProjectID,
				"doc_id":     refKey.DocID,
				"server_seq": c.ServerSeq,
			}).SetUpdate(bson.M{"$set": bson.M{
				"actor_id":        c.ActorID,
				"client_seq":      c.ClientSeq,
				"lamport":         c.Lamport,
				"version_vector":  c.VersionVector,
				"message":         c.Message,
				"operations":      c.Operations,
				"presence_change": c.PresenceChange,
			}}).SetUpsert(true))
		}

		if _, err := c.collection(ColChanges).BulkWrite(
			ctx,
			opModels,
			options.BulkWrite().SetOrdered(false),
		); err != nil {
			return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, err)
		}
	}

	var opStore *ChangeStore
	if cached, ok := c.changeCache.Get(refKey); ok {
		opStore = cached
	} else {
		opStore = NewChangeStore()
		c.changeCache.Add(refKey, opStore)
	}
	opStore.ReplaceOrInsert(opChanges)
	opStore.ExpandRange(ChangeRange{From: initialServerSeq + 1, To: docInfo.ServerSeq})

	// 05. Update the document info with the given changes.
	updateFields := bson.M{
		"server_seq": docInfo.ServerSeq,
	}

	if hasOperations {
		updateFields["updated_at"] = now
	}

	if isRemoved {
		updateFields["removed_at"] = now
	}

	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.DocID,
		"server_seq": initialServerSeq,
	}, bson.M{
		"$set": updateFields,
	})
	if err != nil {
		c.docCache.Remove(refKey)
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, err)
	}
	if res.MatchedCount == 0 {
		c.docCache.Remove(refKey)
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, database.ErrConflictOnUpdate)
	}

	if isRemoved {
		docInfo.RemovedAt = now
	}

	c.docCache.Add(refKey, docInfo)

	return docInfo, checkpoint, nil
}

func (c *Client) CompactChangeInfos(
	ctx context.Context,
	docInfo *database.DocInfo,
	lastServerSeq int64,
	changes []*change.Change,
) error {
	// 1. Purge the resources of the document.
	if _, err := c.purgeDocumentInternals(ctx, docInfo.ProjectID, docInfo.ID); err != nil {
		return err
	}

	// 2. Store compacted change and update document
	newServerSeq := 1
	if len(changes) == 0 {
		newServerSeq = 0
	} else if len(changes) != 1 {
		return fmt.Errorf("compact document of %s: invalid change size %d", docInfo.RefKey(), len(changes))
	}

	for _, cn := range changes {
		encodedOperations, err := database.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}

		if _, err := c.collection(ColChanges).InsertOne(ctx, bson.M{
			"project_id":      docInfo.ProjectID,
			"doc_id":          docInfo.ID,
			"server_seq":      newServerSeq,
			"client_seq":      cn.ClientSeq(),
			"lamport":         cn.ID().Lamport(),
			"actor_id":        types.ID(cn.ID().ActorID().String()),
			"version_vector":  cn.ID().VersionVector(),
			"message":         cn.Message(),
			"operations":      encodedOperations,
			"presence_change": cn.PresenceChange(),
		}); err != nil {
			return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), err)
		}
	}

	// 3. Update document
	c.docCache.Remove(docInfo.RefKey())
	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"project_id": docInfo.ProjectID,
		"_id":        docInfo.ID,
		"server_seq": lastServerSeq,
	}, bson.M{
		"$set": bson.M{
			"server_seq":   newServerSeq,
			"compacted_at": gotime.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%s: %s: %w", docInfo.ProjectID, docInfo.ID, database.ErrConflictOnUpdate)
	}

	return nil
}

// FindLatestChangeInfoByActor returns the latest change created by given actorID.
func (c *Client) FindLatestChangeInfoByActor(
	ctx context.Context,
	docRefKey types.DocRefKey,
	actorID types.ID,
	serverSeq int64,
) (*database.ChangeInfo, error) {
	option := options.FindOne().SetSort(bson.M{
		"server_seq": -1,
	})

	result := c.collection(ColChanges).FindOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
		"actor_id":   actorID,
		"server_seq": bson.M{
			"$lte": serverSeq,
		},
	}, option)

	info := &database.ChangeInfo{}
	if err := result.Decode(info); err != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return info, nil
		}

		return nil, fmt.Errorf("find the latest change of %s: %w", docRefKey, err)
	}

	return info, nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (c *Client) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*change.Change, error) {
	infos, err := c.FindChangeInfosBetweenServerSeqs(ctx, docRefKey, from, to)
	if err != nil {
		return nil, err
	}

	var changes []*change.Change
	for _, info := range infos {
		c, err := info.ToChange()
		if err != nil {
			return nil, err
		}

		changes = append(changes, c)
	}

	return changes, nil
}

// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
func (c *Client) FindChangeInfosBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	if from > to {
		return nil, nil
	}

	// 01. Create a temporary change store to hold the changes.
	store := NewChangeStore()

	// 02. Fill the store with presence only changes.
	if prStore, ok := c.presenceCache.Get(docRefKey); ok {
		store.ReplaceOrInsert(prStore.ChangesInRange(from, to))
	}

	// 03. Fill the store with operation changes. If not cached, load from DB.
	var opStore *ChangeStore
	if cached, ok := c.changeCache.Get(docRefKey); ok {
		opStore = cached
	} else {
		opStore = NewChangeStore()
		c.changeCache.Add(docRefKey, opStore)
	}

	if err := opStore.EnsureChanges(from, to, func(from, to int64) ([]*database.ChangeInfo, error) {
		const chunkSize int64 = 1000
		var infos []*database.ChangeInfo
		current := from
		for current <= to {
			filter := bson.M{
				"project_id": docRefKey.ProjectID,
				"doc_id":     docRefKey.DocID,
				"server_seq": bson.M{"$gte": current, "$lte": to},
			}
			opts := options.Find().SetSort(bson.D{{Key: "server_seq", Value: 1}}).SetLimit(chunkSize)
			cursor, err := c.collection(ColChanges).Find(ctx, filter, opts)
			if err != nil {
				return nil, fmt.Errorf("find changes of %s: %w", docRefKey, err)
			}
			var chunk []*database.ChangeInfo
			if err := cursor.All(ctx, &chunk); err != nil {
				return nil, fmt.Errorf("fetch changes of %s: %w", docRefKey, err)
			}
			if len(chunk) == 0 {
				break
			}
			infos = append(infos, chunk...)
			last := chunk[len(chunk)-1].ServerSeq
			if last >= to || int64(len(chunk)) < chunkSize {
				break
			}
			current = last + 1
		}
		return infos, nil
	}); err != nil {
		return nil, err
	}
	store.ReplaceOrInsert(opStore.ChangesInRange(from, to))

	// 04. Return the changes in the given range.
	return store.ChangesInRange(from, to), nil
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (c *Client) CreateSnapshotInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	doc *document.InternalDocument,
) error {
	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return err
	}

	if _, err := c.collection(ColSnapshots).InsertOne(ctx, bson.M{
		"project_id":     docRefKey.ProjectID,
		"doc_id":         docRefKey.DocID,
		"server_seq":     doc.Checkpoint().ServerSeq,
		"lamport":        doc.Lamport(),
		"version_vector": doc.VersionVector(),
		"snapshot":       snapshot,
		"created_at":     gotime.Now(),
	}); err != nil {
		return fmt.Errorf("create snapshot of %s: %w", docRefKey, err)
	}

	return nil
}

// FindSnapshotInfo returns the snapshot info of the given DocRefKey and serverSeq.
func (c *Client) FindSnapshotInfo(
	ctx context.Context,
	docKey types.DocRefKey,
	serverSeq int64,
) (*database.SnapshotInfo, error) {
	result := c.collection(ColSnapshots).FindOne(ctx, bson.M{
		"project_id": docKey.ProjectID,
		"doc_id":     docKey.DocID,
		"server_seq": serverSeq,
	})

	info := &database.SnapshotInfo{}
	if err := result.Decode(info); err != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return info, nil
		}

		return nil, fmt.Errorf("find snapshot before %d of %s: %w", serverSeq, docKey, err)
	}

	return info, nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (c *Client) FindClosestSnapshotInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	option := options.FindOne().SetSort(bson.M{
		"server_seq": -1,
	})

	if !includeSnapshot {
		option.SetProjection(bson.M{"Snapshot": 0})
	}

	result := c.collection(ColSnapshots).FindOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
		"server_seq": bson.M{
			"$lte": serverSeq,
		},
	}, option)

	info := &database.SnapshotInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			info.VersionVector = time.NewVersionVector()
			return info, nil
		}

		return nil, fmt.Errorf("find snapshot before %d of %s: %w", serverSeq, docRefKey, err)
	}

	return info, nil
}

// UpdateMinVersionVector updates the version vector of the given client
// and returns the minimum version vector of all clients.
func (c *Client) UpdateMinVersionVector(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	// 01. Update synced version vector of the given client and document.
	// NOTE(hackerwins): Considering removing the detached client's lamport
	// from the other clients' version vectors. For now, we just ignore it.
	needsUpdate := true
	if vvMap, ok := c.vectorCache.Get(docRefKey); ok {
		if existing, ok := vvMap.Get(clientInfo.ID); ok && vector.Equal(existing) {
			needsUpdate = false
		}
	}
	if needsUpdate {
		if err := c.updateVersionVector(ctx, clientInfo, docRefKey, vector); err != nil {
			return nil, err
		}
	}

	// 02. Update current client's version vector. If the client is detached, remove it.
	// This is only for the current client and does not affect the version vector of other clients.
	if vvMap, ok := c.vectorCache.Get(docRefKey); ok {
		attached, err := clientInfo.IsAttached(docRefKey.DocID)
		if err != nil {
			return nil, err
		}

		if attached {
			vvMap.Upsert(clientInfo.ID, func(value time.VersionVector, exists bool) time.VersionVector {
				return vector
			})
		} else {
			vvMap.Delete(clientInfo.ID)
		}
	}

	// 03. Calculate the minimum version vector of the given document.
	return c.GetMinVersionVector(ctx, docRefKey, vector)
}

// GetMinVersionVector returns the minimum version vector of the given document.
func (c *Client) GetMinVersionVector(
	ctx context.Context,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	if !c.vectorCache.Contains(docRefKey) {
		var infos []database.VersionVectorInfo
		cursor, err := c.collection(ColVersionVectors).Find(ctx, bson.M{
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
		})
		if err != nil {
			return nil, fmt.Errorf("find min version vector: %w", err)
		}
		if err := cursor.All(ctx, &infos); err != nil {
			return nil, fmt.Errorf("find min version vector: %w", err)
		}

		infoMap := cmap.New[types.ID, time.VersionVector]()
		for i := range infos {
			infoMap.Set(infos[i].ClientID, infos[i].VersionVector)
		}

		c.vectorCache.Add(docRefKey, infoMap)
	}

	vvMap, ok := c.vectorCache.Get(docRefKey)
	if !ok {
		return nil, fmt.Errorf("find min version vector: %w", database.ErrVersionVectorNotFound)
	}

	vals := vvMap.Values()
	vectors := make([]time.VersionVector, len(vals)+1)
	copy(vectors, vals)
	vectors[len(vals)] = vector
	return time.MinVersionVector(vectors...), nil
}

// updateVersionVector updates the given version vector of the given client
func (c *Client) updateVersionVector(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) error {
	isAttached, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	if !isAttached {
		if _, err = c.collection(ColVersionVectors).DeleteOne(ctx, bson.M{
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
			"client_id":  clientInfo.ID,
		}, options.DeleteOne()); err != nil {
			return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
		}
		return nil
	}

	_, err = c.collection(ColVersionVectors).UpdateOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
		"client_id":  clientInfo.ID,
	}, bson.M{
		"$set": bson.M{
			"version_vector": vector,
		},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
	}

	return nil
}

// FindDocInfosByPaging returns the docInfos of the given paging.
func (c *Client) FindDocInfosByPaging(
	ctx context.Context,
	projectID types.ID,
	paging types.Paging[types.ID],
) ([]*database.DocInfo, error) {
	filter := bson.M{
		"project_id": bson.M{
			"$eq": projectID,
		},
		"removed_at": bson.M{
			"$exists": false,
		},
	}
	if paging.Offset != "" {
		k := "$lt"
		if paging.IsForward {
			k = "$gt"
		}
		filter["_id"] = bson.M{
			k: paging.Offset,
		}
	}

	opts := options.Find().SetLimit(int64(paging.PageSize))
	if paging.IsForward {
		opts = opts.SetSort(map[string]int{"_id": 1})
	} else {
		opts = opts.SetSort(map[string]int{"_id": -1})
	}

	cursor, err := c.collection(ColDocuments).Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find documents of %s: %w", projectID, err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("find documents of %s: %w", projectID, err)
	}

	return infos, nil
}

// FindDocInfosByQuery returns the docInfos which match the given query.
func (c *Client) FindDocInfosByQuery(
	ctx context.Context,
	projectID types.ID,
	query string,
	pageSize int,
) (*types.SearchResult[*database.DocInfo], error) {
	cursor, err := c.collection(ColDocuments).Find(ctx, bson.M{
		"project_id": projectID,
		"key": bson.M{"$regex": bson.Regex{
			Pattern: "^" + escapeRegex(query),
		}},
		"removed_at": bson.M{
			"$exists": false,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("find documents by query %s: %w", query, err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("find documents by query %s: %w", query, err)
	}

	limit := pageSize
	limit = min(limit, len(infos))

	return &types.SearchResult[*database.DocInfo]{
		TotalCount: len(infos),
		Elements:   infos[:limit],
	}, nil
}

// IsDocumentAttachedOrAttaching returns whether the given document is attached or attaching to clients.
func (c *Client) IsDocumentAttachedOrAttaching(
	ctx context.Context,
	docRefKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	filter := bson.M{
		"project_id": docRefKey.ProjectID,
		"$or": bson.A{
			bson.M{"attached_docs": docRefKey.DocID},
			bson.M{clientDocInfoKey(docRefKey.DocID, StatusKey): database.DocumentAttaching},
		},
	}

	if excludeClientID != "" {
		filter["_id"] = bson.M{"$ne": excludeClientID}
	}

	result := c.collection(ColClients).FindOne(ctx, filter)

	info := &database.ClientInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return false, nil
		}

		return false, fmt.Errorf("is document %s attached: %w", docRefKey, err)
	}

	return true, nil
}

// CreateSchemaInfo stores the schema of the given document.
func (c *Client) CreateSchemaInfo(
	ctx context.Context,
	projectID types.ID,
	name string,
	version int,
	body string,
	rules []types.Rule,
) (*database.SchemaInfo, error) {
	now := gotime.Now()
	result, err := c.collection(ColSchemas).InsertOne(ctx, bson.M{
		"project_id": projectID,
		"name":       name,
		"version":    version,
		"body":       body,
		"rules":      rules,
		"created_at": now,
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, fmt.Errorf("create schema of %s: %w", name, database.ErrSchemaAlreadyExists)
		}

		return nil, fmt.Errorf("create schema of %s: %w", name, err)
	}

	return &database.SchemaInfo{
		ID:        types.ID(result.InsertedID.(bson.ObjectID).Hex()),
		ProjectID: projectID,
		Name:      name,
		Version:   version,
		Body:      body,
		Rules:     rules,
		CreatedAt: now,
	}, nil
}

// GetSchemaInfo returns the schema of the given document.
func (c *Client) GetSchemaInfo(
	ctx context.Context,
	projectID types.ID,
	name string,
	version int,
) (*database.SchemaInfo, error) {
	result := c.collection(ColSchemas).FindOne(ctx, bson.M{
		"project_id": projectID,
		"name":       name,
		"version":    version,
	})

	info := &database.SchemaInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("find schema of %s@%d: %w", name, version, database.ErrSchemaNotFound)
		}

		return nil, fmt.Errorf("decode schema of %s@%d: %w", name, version, err)
	}

	return info, nil
}

// GetSchemaInfos returns all versions of the schema.
func (c *Client) GetSchemaInfos(
	ctx context.Context,
	projectID types.ID,
	name string,
) ([]*database.SchemaInfo, error) {
	cursor, err := c.collection(ColSchemas).Find(ctx, bson.M{
		"project_id": projectID,
		"name":       name,
	}, options.Find().SetSort(bson.D{{Key: "version", Value: -1}}))
	if err != nil {
		return nil, fmt.Errorf("list schemas of %s: %w", name, err)
	}

	var infos []*database.SchemaInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("list schemas of %s: %w", name, err)
	}

	if len(infos) == 0 {
		return nil, fmt.Errorf("list schemas of %s: %w", name, database.ErrSchemaNotFound)
	}
	return infos, nil
}

func (c *Client) ListSchemaInfos(
	ctx context.Context,
	projectID types.ID,
) ([]*database.SchemaInfo, error) {
	result, err := c.collection(ColSchemas).Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{"project_id": projectID}}},
		bson.D{{Key: "$sort", Value: bson.D{
			{Key: "name", Value: 1},
			{Key: "version", Value: -1},
		}}},
		bson.D{{Key: "$group", Value: bson.M{
			"_id":          "$name",
			"latestSchema": bson.M{"$first": "$$ROOT"},
		}}}})
	if err != nil {
		return nil, fmt.Errorf("list schemas of %s: %w", projectID, err)
	}

	var results []struct {
		LatestSchema *database.SchemaInfo `bson:"latestSchema"`
	}
	if err := result.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("list schemas of %s: %w", projectID, err)
	}

	var infos []*database.SchemaInfo
	for _, result := range results {
		infos = append(infos, result.LatestSchema)
	}

	return infos, nil
}

func (c *Client) RemoveSchemaInfo(
	ctx context.Context,
	projectID types.ID,
	name string,
	version int,
) error {
	rst, err := c.collection(ColSchemas).DeleteOne(ctx, bson.M{
		"project_id": projectID,
		"name":       name,
		"version":    version,
	})
	if err != nil {
		return fmt.Errorf("remove schema %s@%d: %w", name, version, err)
	}

	if rst.DeletedCount == 0 {
		return fmt.Errorf("remove schema %s@%d: %w", name, version, database.ErrSchemaNotFound)
	}

	return nil
}

// PurgeDocument purges the given document and its metadata from the database.
func (c *Client) PurgeDocument(
	ctx context.Context,
	docRefKey types.DocRefKey,
) (map[string]int64, error) {
	res, err := c.purgeDocumentInternals(ctx, docRefKey.ProjectID, docRefKey.DocID)
	if err != nil {
		return nil, err
	}

	if _, err = c.collection(ColDocuments).DeleteOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"_id":        docRefKey.DocID,
	}); err != nil {
		return nil, fmt.Errorf("delete document of %s: %w", docRefKey, err)
	}

	return res, nil
}

func (c *Client) purgeDocumentInternals(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
) (map[string]int64, error) {
	counts := make(map[string]int64)

	c.changeCache.Remove(types.DocRefKey{ProjectID: projectID, DocID: docID})
	c.presenceCache.Remove(types.DocRefKey{ProjectID: projectID, DocID: docID})
	c.vectorCache.Remove(types.DocRefKey{ProjectID: projectID, DocID: docID})

	res, err := c.collection(ColChanges).DeleteMany(ctx, bson.M{
		"project_id": projectID,
		"doc_id":     docID,
	})
	if err != nil {
		return nil, fmt.Errorf("purge changes of %s: %w", docID, err)
	}
	counts[ColChanges] = res.DeletedCount

	res, err = c.collection(ColSnapshots).DeleteMany(ctx, bson.M{
		"project_id": projectID,
		"doc_id":     docID,
	})
	if err != nil {
		return nil, fmt.Errorf("purge snapshots of %s: %w", docID, err)
	}
	counts[ColSnapshots] = res.DeletedCount

	res, err = c.collection(ColVersionVectors).DeleteMany(ctx, bson.M{
		"project_id": projectID,
		"doc_id":     docID,
	})
	if err != nil {
		return nil, fmt.Errorf("purge version vectors of %s: %w", docID, err)
	}
	counts[ColVersionVectors] = res.DeletedCount

	return counts, nil
}

func (c *Client) collection(
	name string,
	opts ...options.Lister[options.CollectionOptions],
) *mongo.Collection {
	return c.client.
		Database(c.config.YorkieDatabase).
		Collection(name, opts...)
}

// escapeRegex escapes special characters by putting a backslash in front of it.
// NOTE(chacha912): (https://github.com/cxr29/scrud/blob/1039f8edaf5eef522275a5a848a0fca0f53224eb/query/util.go#L31-L47)
func escapeRegex(str string) string {
	regex := `\.+*?()|[]{}^$`
	if !strings.ContainsAny(str, regex) {
		return str
	}

	var buf bytes.Buffer
	for _, r := range str {
		if strings.ContainsRune(regex, r) {
			buf.WriteByte('\\')
		}
		buf.WriteByte(byte(r))
	}
	return buf.String()
}

// clientDocInfoKey returns the key for the client document info.
func clientDocInfoKey(docID types.ID, prefix string) string {
	return fmt.Sprintf("documents.%s.%s", docID, prefix)
}

// IsSchemaAttached returns true if the schema is being used by any documents.
func (c *Client) IsSchemaAttached(
	ctx context.Context,
	projectID types.ID,
	schema string,
) (bool, error) {
	filter := bson.M{
		"project_id": projectID,
		"schema":     schema,
	}

	result := c.collection(ColDocuments).FindOne(ctx, filter)

	info := &database.DocInfo{}
	if err := result.Decode(info); err != nil {
		if err == mongo.ErrNoDocuments {
			return false, nil
		}

		return false, fmt.Errorf("is schema %s attached: %w", schema, err)
	}

	return true, nil
}

// CreateRevisionInfo creates a new revision for the given document.
func (c *Client) CreateRevisionInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	label string,
	description string,
	snapshot []byte,
) (*database.RevisionInfo, error) {
	now := gotime.Now()
	info := &database.RevisionInfo{
		ID:          types.NewID(),
		ProjectID:   docRefKey.ProjectID,
		DocID:       docRefKey.DocID,
		Label:       label,
		Description: description,
		Snapshot:    snapshot,
		CreatedAt:   now,
	}

	result, err := c.collection(ColRevisions).InsertOne(ctx, bson.M{
		"_id":         info.ID,
		"project_id":  info.ProjectID,
		"doc_id":      info.DocID,
		"label":       info.Label,
		"description": info.Description,
		"snapshot":    info.Snapshot,
		"created_at":  info.CreatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("insert revision: %w", err)
	}

	info.ID = types.ID(result.InsertedID.(bson.ObjectID).Hex())
	return info, nil
}

// FindRevisionInfosByPaging returns the revision summaries of the given paging.
func (c *Client) FindRevisionInfosByPaging(
	ctx context.Context,
	docRefKey types.DocRefKey,
	paging types.Paging[int],
	includeSnapshot bool,
) ([]*database.RevisionInfo, error) {
	findOptions := options.Find()
	if paging.PageSize > 0 {
		findOptions.SetLimit(int64(paging.PageSize))
	}
	if paging.Offset > 0 {
		findOptions.SetSkip(int64(paging.Offset))
	}

	// Exclude snapshot field if not needed for efficiency
	if !includeSnapshot {
		findOptions.SetProjection(bson.M{"snapshot": 0})
	}

	// Sort by _id descending (newest first, since ObjectId contains timestamp)
	sortOrder := -1
	if paging.IsForward {
		sortOrder = 1
	}
	findOptions.SetSort(bson.D{{Key: "_id", Value: sortOrder}})

	cursor, err := c.collection(ColRevisions).Find(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
	}, findOptions)
	if err != nil {
		return nil, fmt.Errorf("find revisions: %w", err)
	}

	var revisions []*database.RevisionInfo
	if err := cursor.All(ctx, &revisions); err != nil {
		return nil, fmt.Errorf("decode revisions: %w", err)
	}

	return revisions, nil
}

// FindRevisionInfoByID returns a revision by its ID.
func (c *Client) FindRevisionInfoByID(
	ctx context.Context,
	revisionID types.ID,
) (*database.RevisionInfo, error) {
	filter := bson.M{"_id": revisionID}

	result := c.collection(ColRevisions).FindOne(ctx, filter)

	revisionInfo := &database.RevisionInfo{}
	if err := result.Decode(revisionInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, database.ErrRevisionNotFound
		}
		return nil, fmt.Errorf("find revision by id %s: %w", revisionID, err)
	}

	return revisionInfo, nil
}

// DeleteRevisionInfo deletes a revision by its ID.
func (c *Client) DeleteRevisionInfo(
	ctx context.Context,
	revisionID types.ID,
) error {
	filter := bson.M{"_id": revisionID}

	result, err := c.collection(ColRevisions).DeleteOne(ctx, filter)
	if err != nil {
		return fmt.Errorf("delete revision %s: %w", revisionID, err)
	}

	if result.DeletedCount == 0 {
		return database.ErrRevisionNotFound
	}

	return nil
}
