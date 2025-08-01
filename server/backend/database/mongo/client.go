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

	lru "github.com/hashicorp/golang-lru/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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

	docCache        *lru.Cache[types.DocRefKey, *database.DocInfo]
	changeCache     *lru.Cache[types.DocRefKey, *ChangeStore]
	vectorCache     *lru.Cache[types.DocRefKey, *cmap.Map[types.ID, time.VersionVector]]
	clientInfoCache *ClientInfoCache
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
			return nil, fmt.Errorf("parse slow query threshold: %w", err)
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
		return nil, fmt.Errorf("connect to mongo: %w", err)
	}

	pingTimeout := conf.ParsePingTimeout()
	ctxPing, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := client.Ping(ctxPing, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("ping mongo: %w", err)
	}

	if err := ensureIndexes(ctx, client.Database(conf.YorkieDatabase)); err != nil {
		return nil, err
	}

	docCache, err := lru.New[types.DocRefKey, *database.DocInfo](1000)
	if err != nil {
		return nil, fmt.Errorf("initialize docinfo cache: %w", err)
	}

	changeCache, err := lru.New[types.DocRefKey, *ChangeStore](1000)
	if err != nil {
		return nil, fmt.Errorf("initialize change range store: %w", err)
	}

	vectorCache, err := lru.New[types.DocRefKey, *cmap.Map[types.ID, time.VersionVector]](1000)
	if err != nil {
		return nil, fmt.Errorf("initialize version vector cache: %w", err)
	}

	logging.DefaultLogger().Infof("MongoDB connected, URI: %s, DB: %s", conf.ConnectionURI, conf.YorkieDatabase)

	clientInstance := &Client{
		config:      conf,
		client:      client,
		docCache:    docCache,
		changeCache: changeCache,
		vectorCache: vectorCache,
	}

	// Create client info cache with client reference
	clientInfoCache := NewClientInfoCache(nil, clientInstance)

	return &Client{
		config:          conf,
		client:          client,
		docCache:        docCache,
		changeCache:     changeCache,
		vectorCache:     vectorCache,
		clientInfoCache: clientInfoCache,
	}, nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	if err := c.client.Disconnect(context.Background()); err != nil {
		return fmt.Errorf("close mongo client: %w", err)
	}

	c.docCache.Purge()
	c.vectorCache.Purge()

	// Close client info cache
	if c.clientInfoCache != nil {
		if err := c.clientInfoCache.Close(); err != nil {
			return fmt.Errorf("close client info cache: %w", err)
		}
	}

	return nil
}

// TryLeadership attempts to acquire or renew leadership with the given lease duration.
// If leaseToken is empty, it attempts to acquire new leadership.
// If leaseToken is provided, it attempts to renew the existing lease.
func (c *Client) TryLeadership(
	ctx context.Context,
	hostname,
	leaseToken string,
	leaseDuration gotime.Duration,
) (*database.LeadershipInfo, error) {
	now := gotime.Now()
	expiresAt := now.Add(leaseDuration)

	if leaseToken == "" {
		return c.tryAcquireLeadership(ctx, hostname, expiresAt, now)
	}

	return c.tryRenewLeadership(ctx, hostname, leaseToken, expiresAt, now)
}

// FindLeadership returns the current leadership information.
func (c *Client) FindLeadership(
	ctx context.Context,
) (*database.LeadershipInfo, error) {
	result := c.collection(ColLeaderships).FindOne(ctx, bson.M{"singleton": 1})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, nil
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find leadership: %w", result.Err())
	}

	var info database.LeadershipInfo
	if err := result.Decode(&info); err != nil {
		return nil, fmt.Errorf("decode leadership info: %w", err)
	}

	// Check if leadership has expired
	if info.IsExpired() {
		return nil, nil
	}

	return &info, nil
}

// tryAcquireLeadership attempts to acquire new leadership
func (c *Client) tryAcquireLeadership(
	ctx context.Context,
	hostname string,
	expiresAt gotime.Time,
	now gotime.Time,
) (*database.LeadershipInfo, error) {
	// Generate a new lease token
	token, err := database.GenerateLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("generate lease token: %w", err)
	}

	// Try to acquire leadership using atomic upsert.
	result := c.collection(ColLeaderships).FindOneAndUpdate(
		ctx,
		bson.M{
			"$or": []bson.M{
				{"expires_at": bson.M{"$lt": now}}, // Document is expired
				{"term": bson.M{"$exists": false}}, // No document exists
			},
		},
		bson.M{
			"$set": bson.M{
				"hostname":    hostname,
				"lease_token": token,
				"elected_at":  now,
				"expires_at":  expiresAt,
				"renewed_at":  now,
				"singleton":   1,
			},
			"$inc": bson.M{"term": 1},
		},
		options.FindOneAndUpdate().
			SetUpsert(true).
			SetReturnDocument(options.After),
	)

	var info database.LeadershipInfo
	if err := result.Decode(&info); err != nil {
		// If the error is due to a duplicate key, it means another node has
		// already acquired leadership.
		if mongo.IsDuplicateKeyError(err) {
			return c.FindLeadership(ctx)
		}

		return nil, fmt.Errorf("decode new leadership: %w", err)
	}

	// Successfully acquired leadership
	return &info, nil
}

// tryRenewLeadership attempts to renew existing leadership
func (c *Client) tryRenewLeadership(
	ctx context.Context,
	hostname string,
	leaseToken string,
	expiresAt gotime.Time,
	now gotime.Time,
) (*database.LeadershipInfo, error) {
	// Generate a new lease token for renewal
	newLeaseToken, err := database.GenerateLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("generate lease token: %w", err)
	}

	// Try to update the existing leadership with the correct token and hostname.
	result := c.collection(ColLeaderships).FindOneAndUpdate(
		ctx,
		bson.M{
			"singleton":   1,
			"hostname":    hostname,
			"lease_token": leaseToken,
		},
		bson.M{
			"$set": bson.M{
				"lease_token": newLeaseToken,
				"expires_at":  expiresAt,
				"renewed_at":  now,
			},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	)

	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("invalid token or node: %w", database.ErrInvalidLeaseToken)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("renew leadership: %w", result.Err())
	}

	var info database.LeadershipInfo
	if err := result.Decode(&info); err != nil {
		return nil, fmt.Errorf("decode leadership info: %w", err)
	}

	return &info, nil
}

// ClearLeadership removes the current leadership information for testing purposes.
func (c *Client) ClearLeadership(ctx context.Context) error {
	_, err := c.collection(ColLeaderships).DeleteMany(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("clear leadership: %w", err)
	}
	return nil
}

// EnsureDefaultUserAndProject creates the default user and project if they do not exist.
func (c *Client) EnsureDefaultUserAndProject(
	ctx context.Context,
	username,
	password string,
	clientDeactivateThreshold string,
) (*database.UserInfo, *database.ProjectInfo, error) {
	userInfo, err := c.ensureDefaultUserInfo(ctx, username, password)
	if err != nil {
		return nil, nil, err
	}

	projectInfo, err := c.ensureDefaultProjectInfo(ctx, userInfo.ID, clientDeactivateThreshold)
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
		return nil, fmt.Errorf("upsert default user info: %w", err)
	}

	result := c.collection(ColUsers).FindOne(ctx, bson.M{
		"username": candidate.Username,
	})

	info := database.UserInfo{}
	if err := result.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("default: %w", database.ErrUserNotFound)
		}
		return nil, fmt.Errorf("decode user info: %w", err)
	}

	return &info, nil
}

// ensureDefaultProjectInfo creates the default project info if it does not exist.
func (c *Client) ensureDefaultProjectInfo(
	ctx context.Context,
	defaultUserID types.ID,
	defaultClientDeactivateThreshold string,
) (*database.ProjectInfo, error) {
	candidate := database.NewProjectInfo(database.DefaultProjectName, defaultUserID, defaultClientDeactivateThreshold)
	candidate.ID = database.DefaultProjectID

	_, err := c.collection(ColProjects).UpdateOne(ctx, bson.M{
		"_id": candidate.ID,
	}, bson.M{
		"$setOnInsert": bson.M{
			"name":                         candidate.Name,
			"owner":                        candidate.Owner,
			"client_deactivate_threshold":  candidate.ClientDeactivateThreshold,
			"max_subscribers_per_document": candidate.MaxSubscribersPerDocument,
			"max_attachments_per_document": candidate.MaxAttachmentsPerDocument,
			"max_size_per_document":        candidate.MaxSizePerDocument,
			"public_key":                   candidate.PublicKey,
			"secret_key":                   candidate.SecretKey,
			"created_at":                   candidate.CreatedAt,
		},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("create default project: %w", err)
	}

	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"_id": candidate.ID,
	})

	info := database.ProjectInfo{}
	if err := result.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("default: %w", database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &info, nil
}

// CreateProjectInfo creates a new project.
func (c *Client) CreateProjectInfo(
	ctx context.Context,
	name string,
	owner types.ID,
	clientDeactivateThreshold string,
) (*database.ProjectInfo, error) {
	info := database.NewProjectInfo(name, owner, clientDeactivateThreshold)
	result, err := c.collection(ColProjects).InsertOne(ctx, bson.M{
		"name":                        info.Name,
		"owner":                       owner,
		"client_deactivate_threshold": info.ClientDeactivateThreshold,
		"public_key":                  info.PublicKey,
		"secret_key":                  info.SecretKey,
		"created_at":                  info.CreatedAt,
		"max_size_per_document":       info.MaxSizePerDocument,
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, database.ErrProjectAlreadyExists
		}

		return nil, fmt.Errorf("create project info: %w", err)
	}

	info.ID = types.ID(result.InsertedID.(bson.ObjectID).Hex())
	return info, nil
}

// FindNextNCyclingProjectInfos finds the next N cycling projects from the given projectID.
func (c *Client) FindNextNCyclingProjectInfos(
	ctx context.Context,
	pageSize int,
	lastProjectID types.ID,
) ([]*database.ProjectInfo, error) {
	opts := options.Find()
	opts.SetLimit(int64(pageSize))

	cursor, err := c.collection(ColProjects).Find(ctx, bson.M{
		"_id": bson.M{
			"$gt": lastProjectID,
		},
	}, opts)
	if err != nil {
		return nil, fmt.Errorf("find project infos: %w", err)
	}

	var infos []*database.ProjectInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch project infos: %w", err)
	}

	if len(infos) < pageSize {
		opts.SetLimit(int64(pageSize - len(infos)))

		cursor, err := c.collection(ColProjects).Find(ctx, bson.M{
			"_id": bson.M{
				"$lte": lastProjectID,
			},
		}, opts)
		if err != nil {
			return nil, fmt.Errorf("find project infos: %w", err)
		}

		var newInfos []*database.ProjectInfo
		if err := cursor.All(ctx, &newInfos); err != nil {
			return nil, fmt.Errorf("fetch project infos: %w", err)
		}
		infos = append(infos, newInfos...)
	}

	return infos, nil
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
		return nil, fmt.Errorf("fetch project infos: %w", err)
	}

	var infos []*database.ProjectInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch project infos: %w", err)
	}

	return infos, nil
}

// FindProjectInfoByPublicKey returns a project by public key.
func (c *Client) FindProjectInfoByPublicKey(ctx context.Context, publicKey string) (*database.ProjectInfo, error) {
	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"public_key": publicKey,
	})

	projectInfo := database.ProjectInfo{}
	if err := result.Decode(&projectInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", publicKey, database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &projectInfo, nil
}

// FindProjectInfoBySecretKey returns a project by secret key.
func (c *Client) FindProjectInfoBySecretKey(ctx context.Context, secretKey string) (*database.ProjectInfo, error) {
	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"secret_key": secretKey,
	})

	projectInfo := database.ProjectInfo{}
	if err := result.Decode(&projectInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", secretKey, database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &projectInfo, nil
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

	projectInfo := database.ProjectInfo{}
	if err := result.Decode(&projectInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", name, database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &projectInfo, nil
}

// FindProjectInfoByID returns a project by the given id.
func (c *Client) FindProjectInfoByID(ctx context.Context, id types.ID) (*database.ProjectInfo, error) {
	result := c.collection(ColProjects).FindOne(ctx, bson.M{
		"_id": id,
	})

	projectInfo := database.ProjectInfo{}
	if err := result.Decode(&projectInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &projectInfo, nil
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

	info := database.ProjectInfo{}
	if err := res.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		if mongo.IsDuplicateKeyError(err) {
			return nil, fmt.Errorf("%s: %w", *fields.Name, database.ErrProjectNameAlreadyExists)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &info, nil
}

// RotateProjectKeys rotates the API keys of the project.
func (c *Client) RotateProjectKeys(
	ctx context.Context,
	owner types.ID,
	id types.ID,
	publicKey string,
	secretKey string,
) (*database.ProjectInfo, error) {
	// Update project with new keys
	res := c.collection(ColProjects).FindOneAndUpdate(ctx, bson.M{
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
	info := database.ProjectInfo{}
	if err := res.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		return nil, fmt.Errorf("decode project info: %w", err)
	}

	return &info, nil
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

		return nil, fmt.Errorf("create user info: %w", err)
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

	info := database.UserInfo{}
	if err := result.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", githubID, database.ErrUserNotFound)
		}
		return nil, fmt.Errorf("decode user info: %w", err)
	}

	return &info, nil
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
		return fmt.Errorf("no user found with username %s", username)
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
		return fmt.Errorf("no user found with username %s", username)
	}
	return nil
}

// FindUserInfoByID returns a user by ID.
func (c *Client) FindUserInfoByID(ctx context.Context, clientID types.ID) (*database.UserInfo, error) {
	result := c.collection(ColUsers).FindOne(ctx, bson.M{
		"_id": clientID,
	})

	userInfo := database.UserInfo{}
	if err := result.Decode(&userInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", clientID, database.ErrUserNotFound)
		}
		return nil, fmt.Errorf("decode user info: %w", err)
	}

	return &userInfo, nil
}

// FindUserInfoByName returns a user by username.
func (c *Client) FindUserInfoByName(ctx context.Context, username string) (*database.UserInfo, error) {
	result := c.collection(ColUsers).FindOne(ctx, bson.M{
		"username": username,
	})

	userInfo := database.UserInfo{}
	if err := result.Decode(&userInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", username, database.ErrUserNotFound)
		}
		return nil, fmt.Errorf("decode user info: %w", err)
	}

	return &userInfo, nil
}

// ListUserInfos returns all users.
func (c *Client) ListUserInfos(
	ctx context.Context,
) ([]*database.UserInfo, error) {
	cursor, err := c.collection(ColUsers).Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("list user infos: %w", err)
	}

	var infos []*database.UserInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch all user infos: %w", err)
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

	// 1. Check cache first for existing client
	// Try to find existing client by key in cache
	var existingClient *database.ClientInfo
	for refKey, cached := range c.clientInfoCache.cache {
		if refKey.ProjectID == projectID && cached.ClientInfo.Key == key {
			existingClient = cached.ClientInfo
			break
		}
	}

	if existingClient != nil {
		// If cache has dirty data, flush it first to ensure consistency
		if c.clientInfoCache.IsDirty(existingClient.RefKey()) {
			if err := c.clientInfoCache.FlushToDB(); err != nil {
				return nil, fmt.Errorf("flush dirty cache: %w", err)
			}
		}

		// Update client status in cache
		existingClient.Status = database.ClientActivated
		existingClient.UpdatedAt = now
		existingClient.Metadata = metadata
		c.clientInfoCache.UpdateClientInfo(existingClient.RefKey(), existingClient)

		// Flush immediately for activation
		if err := c.clientInfoCache.FlushToDB(); err != nil {
			return nil, fmt.Errorf("flush activated client: %w", err)
		}

		return existingClient, nil
	}

	// 2. If not in cache, proceed with DB operation
	res, err := c.collection(ColClients).UpdateOne(ctx, bson.M{
		"project_id": projectID,
		"key":        key,
		"metadata":   metadata,
	}, bson.M{
		"$set": bson.M{
			StatusKey:    database.ClientActivated,
			"updated_at": now,
		},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("upsert client: %w", err)
	}

	var result *mongo.SingleResult
	if res.UpsertedCount > 0 {
		result = c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
			"project_id": projectID,
			"_id":        res.UpsertedID,
		}, bson.M{
			"$set": bson.M{
				"created_at": now,
			},
		})
	} else {
		result = c.collection(ColClients).FindOne(ctx, bson.M{
			"project_id": projectID,
			"key":        key,
		})
	}

	clientInfo := database.ClientInfo{}
	if err = result.Decode(&clientInfo); err != nil {
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	// Invalidate cache for this client
	c.clientInfoCache.Invalidate(clientInfo.RefKey())

	return &clientInfo, nil
}

// TryAttaching updates the status of the document to Attaching to prevent
// deactivating the client while the document is being attached.
func (c *Client) TryAttaching(
	ctx context.Context,
	refKey types.ClientRefKey,
	docID types.ID,
) (*database.ClientInfo, error) {
	// 1. Check cache first for latest client info
	if cached := c.clientInfoCache.Get(refKey); cached != nil {
		// If cache has dirty data, flush it first to ensure consistency
		if c.clientInfoCache.IsDirty(refKey) {
			if err := c.clientInfoCache.FlushToDB(); err != nil {
				return nil, fmt.Errorf("flush dirty cache: %w", err)
			}
		}

		// Check if client is activated
		if cached.Status != database.ClientActivated {
			return nil, fmt.Errorf("try to attach document: %w", database.ErrClientNotActivated)
		}

		// Check if document is already attached
		if docInfo, exists := cached.Documents[docID]; exists && docInfo.Status == database.DocumentAttached {
			return nil, fmt.Errorf("try to attach document: %w", database.ErrDocumentAlreadyAttached)
		}

		// Update document status in cache
		if cached.Documents == nil {
			cached.Documents = make(map[types.ID]*database.ClientDocInfo)
		}
		cached.Documents[docID] = &database.ClientDocInfo{
			Status:    database.DocumentAttaching,
			ServerSeq: 0,
			ClientSeq: 0,
		}
		cached.UpdatedAt = gotime.Now()
		c.clientInfoCache.UpdateClientInfo(refKey, cached)

		// Flush immediately for attachment
		if err := c.clientInfoCache.FlushToDB(); err != nil {
			return nil, fmt.Errorf("flush attaching client: %w", err)
		}

		return cached, nil
	}

	// 2. If not in cache, proceed with DB operation
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
			return nil, fmt.Errorf("try to attach document: %w", database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	// Update cache with the new client info
	c.clientInfoCache.Set(refKey, info)

	return info, nil
}

// DeactivateClient deactivates the client of the given refKey.
func (c *Client) DeactivateClient(
	ctx context.Context,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	now := gotime.Now()

	// 1. Check cache first for latest client info
	if cached := c.clientInfoCache.Get(refKey); cached != nil {
		// If cache has dirty data, flush it first to ensure consistency
		if c.clientInfoCache.IsDirty(refKey) {
			if err := c.clientInfoCache.FlushToDB(); err != nil {
				return nil, fmt.Errorf("flush dirty cache: %w", err)
			}
		}

		// Check if client is activated
		if cached.Status != database.ClientActivated {
			return nil, fmt.Errorf(
				"conditions not satisfied to deactivate client: %w",
				database.ErrClientNotActivated,
			)
		}

		// Check if any documents are attached
		for _, docInfo := range cached.Documents {
			if docInfo.Status == database.DocumentAttaching || docInfo.Status == database.DocumentAttached {
				return nil, fmt.Errorf(
					"conditions not satisfied to deactivate client: %w",
					database.ErrAttachedDocumentExists,
				)
			}
		}

		// Update client status in cache
		cached.Status = database.ClientDeactivated
		cached.UpdatedAt = now
		c.clientInfoCache.UpdateClientInfo(refKey, cached)

		// Flush immediately for deactivation
		if err := c.clientInfoCache.FlushToDB(); err != nil {
			return nil, fmt.Errorf("flush deactivated client: %w", err)
		}

		return cached, nil
	}

	// 2. If not in cache, proceed with DB operation
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

	info := database.ClientInfo{}
	if err := result.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf(
				"conditions not satisfied to deactivate client: %w",
				database.ErrClientNotFound,
			)
		}
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	// Invalidate cache for this client
	c.clientInfoCache.Invalidate(refKey)

	return &info, nil
}

// FindClientInfoByRefKey finds the client of the given refKey.
func (c *Client) FindClientInfoByRefKey(ctx context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	// 1. Try to get from cache first
	if cached := c.clientInfoCache.Get(refKey); cached != nil {
		// If cache has dirty data, ensure it's flushed for consistency
		if c.clientInfoCache.IsDirty(refKey) {
			if err := c.clientInfoCache.FlushToDB(); err != nil {
				return nil, fmt.Errorf("flush dirty cache: %w", err)
			}
		}
		return cached, nil
	}

	// 2. If not in cache, get from database
	result := c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.ClientID,
	}, bson.M{
		"$set": bson.M{
			"updated_at": gotime.Now(),
		},
	})

	clientInfo := database.ClientInfo{}
	if err := result.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", refKey, database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	// 3. Store in cache
	c.clientInfoCache.Set(refKey, &clientInfo)

	return &clientInfo, nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (c *Client) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	// Validate document attachment status
	docRefKey := docInfo.RefKey()
	_, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	// Validate client exists in database
	result := c.collection(ColClients).FindOne(ctx, bson.M{
		"project_id": clientInfo.ProjectID,
		"_id":        clientInfo.ID,
	})
	if result.Err() == mongo.ErrNoDocuments {
		return fmt.Errorf("%s: %w", clientInfo.ID, database.ErrClientNotFound)
	}
	if result.Err() != nil {
		return fmt.Errorf("find client by id: %w", result.Err())
	}

	// Update cache only - write-back strategy maintains performance
	c.clientInfoCache.UpdateClientInfo(clientInfo.RefKey(), clientInfo)

	// For critical operations that need immediate consistency,
	// flush dirty data to ensure subsequent operations see the latest state
	if c.clientInfoCache.IsDirty(clientInfo.RefKey()) {
		if err := c.clientInfoCache.FlushToDB(); err != nil {
			return fmt.Errorf("flush client info to DB: %w", err)
		}
	}

	return nil
}

// FindDeactivateCandidatesPerProject finds the clients that need housekeeping per project.
func (c *Client) FindDeactivateCandidatesPerProject(
	ctx context.Context,
	project *database.ProjectInfo,
	candidatesLimit int,
) ([]*database.ClientInfo, error) {
	clientDeactivateThreshold, err := project.ClientDeactivateThresholdAsTimeDuration()
	if err != nil {
		return nil, err
	}

	cursor, err := c.collection(ColClients).Find(ctx, bson.M{
		"project_id": project.ID,
		StatusKey:    database.ClientActivated,
		"updated_at": bson.M{
			"$lte": gotime.Now().Add(-clientDeactivateThreshold),
		},
	}, options.Find().SetLimit(int64(candidatesLimit)))

	if err != nil {
		return nil, fmt.Errorf("find deactivate candidates: %w", err)
	}

	var clientInfos []*database.ClientInfo
	if err := cursor.All(ctx, &clientInfos); err != nil {
		return nil, fmt.Errorf("fetch deactivate candidates: %w", err)
	}

	return clientInfos, nil
}

// FindCompactionCandidatesPerProject finds the documents that need compaction per project.
func (c *Client) FindCompactionCandidatesPerProject(
	ctx context.Context,
	project *database.ProjectInfo,
	candidatesLimit int,
	compactionMinChanges int,
) ([]*database.DocInfo, error) {
	cursor, err := c.collection(ColDocuments).Find(ctx, bson.M{
		"project_id": project.ID,
	}, options.Find().SetLimit(int64(candidatesLimit*2)))
	if err != nil {
		return nil, fmt.Errorf("find documents: %w", err)
	}
	defer func() {
		if err := cursor.Close(ctx); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	var infos []*database.DocInfo
	for cursor.Next(ctx) {
		var info database.DocInfo
		if err := cursor.Decode(&info); err != nil {
			return nil, fmt.Errorf("decode document: %w", err)
		}

		if candidatesLimit <= len(infos) {
			break
		}

		// 1. Check if the document is attached to a client.
		// TODO(chacha912): Resolve the N+1 problem.
		isAttached, err := c.IsDocumentAttached(ctx, types.DocRefKey{
			ProjectID: project.ID,
			DocID:     info.ID,
		}, "")
		if err != nil {
			return nil, err
		}
		if isAttached {
			continue
		}

		// 2. Check if the document has enough changes to compact.
		if info.ServerSeq < int64(compactionMinChanges) {
			continue
		}

		infos = append(infos, &info)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error: %w", err)
	}

	return infos, nil
}

// FindAttachedClientInfosByRefKey returns the client infos of the given document.
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
		return nil, fmt.Errorf("find client infos: %w", err)
	}

	var clientInfos []*database.ClientInfo
	if err := cursor.All(ctx, &clientInfos); err != nil {
		return nil, fmt.Errorf("fetch client infos: %w", err)
	}

	return clientInfos, nil
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
		return nil, fmt.Errorf("decode document: %w", err)
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
		return nil, fmt.Errorf("find document: %w", result.Err())
	}

	info := database.DocInfo{}
	if err := result.Decode(&info); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &info, nil
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
		return nil, fmt.Errorf("find documents: %w", err)
	}

	var docInfos []*database.DocInfo
	if err := cursor.All(ctx, &docInfos); err != nil {
		return nil, fmt.Errorf("fetch documents: %w", err)
	}

	return docInfos, nil
}

// FindDocInfoByRefKey finds a docInfo of the given refKey.
func (c *Client) FindDocInfoByRefKey(
	ctx context.Context,
	refKey types.DocRefKey,
) (*database.DocInfo, error) {
	result := c.collection(ColDocuments).FindOne(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.DocID,
	})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("%s: %w", refKey, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find document: %w", result.Err())
	}

	info := database.DocInfo{}
	if err := result.Decode(&info); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &info, nil
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
		return fmt.Errorf("%s: %w", refKey, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return fmt.Errorf("update document info status to removed: %w", result.Err())
	}

	// Invalidate all client caches that might have this document attached
	// This is a conservative approach - we could be more selective but it's safer
	c.clientInfoCache.InvalidateAll()

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
		return fmt.Errorf("%s: %w", refKey, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return fmt.Errorf("update document schema: %w", result.Err())
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
		return 0, fmt.Errorf("count documents: %w", err)
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
		return 0, fmt.Errorf("count clients: %w", err)
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
	cached, ok := c.docCache.Get(refKey)
	if !ok {
		info, err := c.FindDocInfoByRefKey(ctx, refKey)
		if err != nil {
			return nil, change.InitialCheckpoint, err
		}
		c.docCache.Add(refKey, info)
		cached = info
	}
	docInfo := cached.DeepCopy()

	// 01. Fetch the document info.
	if len(changes) == 0 && !isRemoved {
		return docInfo, checkpoint, nil
	}

	// 02. Optimized batch processing
	initialServerSeq := docInfo.ServerSeq
	now := gotime.Now()

	// Pre-allocate models slice to avoid dynamic allocations
	models := make([]mongo.WriteModel, 0, len(changes))
	hasOperations := false

	for _, cn := range changes {
		serverSeq := docInfo.IncreaseServerSeq()
		checkpoint = checkpoint.NextServerSeq(serverSeq)
		cn.ServerSeq = serverSeq
		checkpoint = checkpoint.SyncClientSeq(cn.ClientSeq)

		if len(cn.Operations) > 0 {
			hasOperations = true
		}

		models = append(models, mongo.NewUpdateOneModel().SetFilter(bson.M{
			"project_id": refKey.ProjectID,
			"doc_id":     refKey.DocID,
			"server_seq": cn.ServerSeq,
		}).SetUpdate(bson.M{"$set": bson.M{
			"actor_id":        cn.ActorID,
			"client_seq":      cn.ClientSeq,
			"lamport":         cn.Lamport,
			"version_vector":  cn.VersionVector,
			"message":         cn.Message,
			"operations":      cn.Operations,
			"presence_change": cn.PresenceChange,
		}}).SetUpsert(true))
	}

	if len(changes) > 0 {
		if _, err := c.collection(ColChanges).BulkWrite(
			ctx,
			models,
			options.BulkWrite().SetOrdered(false),
		); err != nil {
			return nil, change.InitialCheckpoint, fmt.Errorf("bulk write changes: %w", err)
		}
	}

	// 03. Update the document info with the given changes.
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
		return nil, change.InitialCheckpoint, fmt.Errorf("update document: %w", err)
	}
	if res.MatchedCount == 0 {
		c.docCache.Remove(refKey)
		return nil, change.InitialCheckpoint, fmt.Errorf("%s: %w", refKey, database.ErrConflictOnUpdate)
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
		return fmt.Errorf("invalid number of changes: %d", len(changes))
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
			return fmt.Errorf("store change: %w", err)
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
		return fmt.Errorf("update document: %w", err)
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

	changeInfo := &database.ChangeInfo{}
	if result.Err() == mongo.ErrNoDocuments {
		return changeInfo, nil
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find change: %w", result.Err())
	}

	if err := result.Decode(changeInfo); err != nil {
		return nil, fmt.Errorf("decode change: %w", err)
	}

	return changeInfo, nil
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

	// Get or create a change range store for this document
	var store *ChangeStore
	if cached, ok := c.changeCache.Get(docRefKey); ok {
		store = cached
	} else {
		store = NewChangeStore()
		c.changeCache.Add(docRefKey, store)
	}

	// Calculate missing ranges and fetch them in a single operation
	if err := store.EnsureChanges(from, to, func(from, to int64) ([]*database.ChangeInfo, error) {
		cursor, err := c.collection(ColChanges).Find(ctx, bson.M{
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
			"server_seq": bson.M{
				"$gte": from,
				"$lte": to,
			},
		}, options.Find())
		if err != nil {
			return nil, fmt.Errorf("find changes: %w", err)
		}

		var infos []*database.ChangeInfo
		if err := cursor.All(ctx, &infos); err != nil {
			return nil, fmt.Errorf("fetch changes: %w", err)
		}

		return infos, nil
	}); err != nil {
		return nil, err
	}

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
		return fmt.Errorf("insert snapshot: %w", err)
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

	snapshotInfo := &database.SnapshotInfo{}
	if result.Err() == mongo.ErrNoDocuments {
		return snapshotInfo, nil
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find snapshot: %w", result.Err())
	}

	if err := result.Decode(snapshotInfo); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}

	return snapshotInfo, nil
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

	snapshotInfo := &database.SnapshotInfo{}
	if result.Err() == mongo.ErrNoDocuments {
		snapshotInfo.VersionVector = time.NewVersionVector()
		return snapshotInfo, nil
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find snapshot: %w", result.Err())
	}

	if err := result.Decode(snapshotInfo); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}

	return snapshotInfo, nil
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
	if err := c.updateVersionVector(ctx, clientInfo, docRefKey, vector); err != nil {
		return nil, err
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
			vvMap.Delete(clientInfo.ID, func(value time.VersionVector, exists bool) bool {
				return exists
			})
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
			return nil, fmt.Errorf("find all version vectors: %w", err)
		}
		if err := cursor.All(ctx, &infos); err != nil {
			return nil, fmt.Errorf("decode version vectors: %w", err)
		}

		infoMap := cmap.New[types.ID, time.VersionVector]()
		for i := range infos {
			infoMap.Set(infos[i].ClientID, infos[i].VersionVector)
		}

		c.vectorCache.Add(docRefKey, infoMap)
	}

	vvMap, ok := c.vectorCache.Get(docRefKey)
	if !ok {
		return nil, fmt.Errorf("version vectors from cache: %w", database.ErrVersionVectorNotFound)
	}

	vectors := append(vvMap.Values(), vector)
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
			return fmt.Errorf("delete version vector: %w", err)
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
		return fmt.Errorf("update version vector: %w", err)
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
		return nil, fmt.Errorf("find documents: %w", err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch document infos: %w", err)
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
		return nil, fmt.Errorf("find document infos: %w", err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch documents: %w", err)
	}

	limit := pageSize
	limit = min(limit, len(infos))

	return &types.SearchResult[*database.DocInfo]{
		TotalCount: len(infos),
		Elements:   infos[:limit],
	}, nil
}

// IsDocumentAttached returns whether the given document is attached to clients.
func (c *Client) IsDocumentAttached(
	ctx context.Context,
	docRefKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	filter := bson.M{
		"project_id":    docRefKey.ProjectID,
		"attached_docs": docRefKey.DocID,
	}

	if excludeClientID != "" {
		filter["_id"] = bson.M{"$ne": excludeClientID}
	}

	result := c.collection(ColClients).FindOne(ctx, filter)
	if result.Err() == mongo.ErrNoDocuments {
		return false, nil
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
			return nil, database.ErrSchemaAlreadyExists
		}

		return nil, fmt.Errorf("create schema info: %w", err)
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
	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("%s %d: %w", name, version, database.ErrSchemaNotFound)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find schema: %w", result.Err())
	}

	if err := result.Decode(info); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
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
		return nil, fmt.Errorf("find schema: %w", err)
	}

	var infos []*database.SchemaInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	if len(infos) == 0 {
		return nil, fmt.Errorf("%s: %w", name, database.ErrSchemaNotFound)
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
		return nil, fmt.Errorf("aggregate schema: %w", err)
	}

	var results []struct {
		LatestSchema *database.SchemaInfo `bson:"latestSchema"`
	}
	if err := result.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
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
		return fmt.Errorf("delete schema: %w", err)
	}

	if rst.DeletedCount == 0 {
		return fmt.Errorf("%s %d: %w", name, version, database.ErrSchemaNotFound)
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
		return nil, fmt.Errorf("delete document: %w", err)
	}

	// Invalidate all client caches since the document is completely removed
	c.clientInfoCache.InvalidateAll()

	return res, nil
}

func (c *Client) purgeDocumentInternals(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
) (map[string]int64, error) {
	counts := make(map[string]int64)

	c.changeCache.Remove(types.DocRefKey{ProjectID: projectID, DocID: docID})
	res, err := c.collection(ColChanges).DeleteMany(ctx, bson.M{
		"project_id": projectID,
		"doc_id":     docID,
	})
	if err != nil {
		return nil, fmt.Errorf("purge changes: %w", err)
	}
	counts[ColChanges] = res.DeletedCount

	res, err = c.collection(ColSnapshots).DeleteMany(ctx, bson.M{
		"project_id": projectID,
		"doc_id":     docID,
	})
	if err != nil {
		return nil, fmt.Errorf("purge snapshots: %w", err)
	}
	counts[ColSnapshots] = res.DeletedCount

	res, err = c.collection(ColVersionVectors).DeleteMany(ctx, bson.M{
		"project_id": projectID,
		"doc_id":     docID,
	})
	if err != nil {
		return nil, fmt.Errorf("purge version vectors: %w", err)
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
	if result.Err() == mongo.ErrNoDocuments {
		return false, nil
	}
	return true, nil
}
