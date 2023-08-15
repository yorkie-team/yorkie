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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Client is a client that connects to Mongo DB and reads or saves Yorkie data.
type Client struct {
	config *Config
	client *mongo.Client
}

// Dial creates an instance of Client and dials the given MongoDB.
func Dial(conf *Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), conf.ParseConnectionTimeout())
	defer cancel()

	client, err := mongo.Connect(
		ctx,
		options.Client().
			ApplyURI(conf.ConnectionURI).
			SetRegistry(newRegistryBuilder().Build()),
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

	logging.DefaultLogger().Infof("MongoDB connected, URI: %s, DB: %s", conf.ConnectionURI, conf.YorkieDatabase)

	return &Client{
		config: conf,
		client: client,
	}, nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	if err := c.client.Disconnect(context.Background()); err != nil {
		return fmt.Errorf("close mongo client: %w", err)
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

	_, err = c.collection(colUsers).UpdateOne(ctx, bson.M{
		"username": candidate.Username,
	}, bson.M{
		"$setOnInsert": bson.M{
			"username":        candidate.Username,
			"hashed_password": candidate.HashedPassword,
			"created_at":      candidate.CreatedAt,
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("upsert default user info: %w", err)
	}

	result := c.collection(colUsers).FindOne(ctx, bson.M{
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
	encodedID, err := encodeID(candidate.ID)
	if err != nil {
		return nil, err
	}
	encodedDefaultUserID, err := encodeID(defaultUserID)
	if err != nil {
		return nil, err
	}

	_, err = c.collection(colProjects).UpdateOne(ctx, bson.M{
		"_id": encodedID,
	}, bson.M{
		"$setOnInsert": bson.M{
			"name":                        candidate.Name,
			"owner":                       encodedDefaultUserID,
			"client_deactivate_threshold": candidate.ClientDeactivateThreshold,
			"public_key":                  candidate.PublicKey,
			"secret_key":                  candidate.SecretKey,
			"created_at":                  candidate.CreatedAt,
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("create default project: %w", err)
	}

	result := c.collection(colProjects).FindOne(ctx, bson.M{
		"_id": encodedID,
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
	encodedOwner, err := encodeID(owner)
	if err != nil {
		return nil, err
	}

	info := database.NewProjectInfo(name, owner, clientDeactivateThreshold)
	result, err := c.collection(colProjects).InsertOne(ctx, bson.M{
		"name":                        info.Name,
		"owner":                       encodedOwner,
		"client_deactivate_threshold": info.ClientDeactivateThreshold,
		"public_key":                  info.PublicKey,
		"secret_key":                  info.SecretKey,
		"created_at":                  info.CreatedAt,
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, database.ErrProjectAlreadyExists
		}

		return nil, fmt.Errorf("create project info: %w", err)
	}

	info.ID = types.ID(result.InsertedID.(primitive.ObjectID).Hex())
	return info, nil
}

// listProjectInfos returns all project infos rotationally.
func (c *Client) listProjectInfos(
	ctx context.Context,
	pageSize int,
	housekeepingLastProjectID types.ID,
) ([]*database.ProjectInfo, error) {
	encodedID, err := encodeID(housekeepingLastProjectID)
	if err != nil {
		return nil, err
	}

	opts := options.Find()
	opts.SetLimit(int64(pageSize))

	cursor, err := c.collection(colProjects).Find(ctx, bson.M{
		"_id": bson.M{
			"$gt": encodedID,
		},
	}, opts)
	if err != nil {
		return nil, fmt.Errorf("find project infos: %w", err)
	}

	var infos []*database.ProjectInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch project infos: %w", err)
	}

	return infos, nil
}

// ListProjectInfos returns all project infos owned by owner.
func (c *Client) ListProjectInfos(
	ctx context.Context,
	owner types.ID,
) ([]*database.ProjectInfo, error) {
	encodedOwnerID, err := encodeID(owner)
	if err != nil {
		return nil, err
	}

	cursor, err := c.collection(colProjects).Find(ctx, bson.M{
		"owner": encodedOwnerID,
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
	result := c.collection(colProjects).FindOne(ctx, bson.M{
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

// FindProjectInfoByName returns a project by name.
func (c *Client) FindProjectInfoByName(
	ctx context.Context,
	owner types.ID,
	name string,
) (*database.ProjectInfo, error) {
	encodedOwner, err := encodeID(owner)
	if err != nil {
		return nil, err
	}

	result := c.collection(colProjects).FindOne(ctx, bson.M{
		"name":  name,
		"owner": encodedOwner,
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
	encodedID, err := encodeID(id)
	if err != nil {
		return nil, err
	}

	result := c.collection(colProjects).FindOne(ctx, bson.M{
		"_id": encodedID,
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
	encodedOwner, err := encodeID(owner)
	if err != nil {
		return nil, err
	}
	encodedID, err := encodeID(id)
	if err != nil {
		return nil, err
	}

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

	res := c.collection(colProjects).FindOneAndUpdate(ctx, bson.M{
		"_id":   encodedID,
		"owner": encodedOwner,
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

// CreateUserInfo creates a new user.
func (c *Client) CreateUserInfo(
	ctx context.Context,
	username string,
	hashedPassword string,
) (*database.UserInfo, error) {
	info := database.NewUserInfo(username, hashedPassword)
	result, err := c.collection(colUsers).InsertOne(ctx, bson.M{
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

	info.ID = types.ID(result.InsertedID.(primitive.ObjectID).Hex())
	return info, nil
}

// FindUserInfo returns a user by username.
func (c *Client) FindUserInfo(ctx context.Context, username string) (*database.UserInfo, error) {
	result := c.collection(colUsers).FindOne(ctx, bson.M{
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
	cursor, err := c.collection(colUsers).Find(ctx, bson.M{})
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
func (c *Client) ActivateClient(ctx context.Context, projectID types.ID, key string) (*database.ClientInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}

	now := gotime.Now()
	res, err := c.collection(colClients).UpdateOne(ctx, bson.M{
		"project_id": encodedProjectID,
		"key":        key,
	}, bson.M{
		"$set": bson.M{
			"status":     database.ClientActivated,
			"updated_at": now,
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		return nil, fmt.Errorf("upsert client: %w", err)
	}

	var result *mongo.SingleResult
	if res.UpsertedCount > 0 {
		result = c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
			"_id": res.UpsertedID,
		}, bson.M{
			"$set": bson.M{
				"created_at": now,
			},
		})
	} else {
		result = c.collection(colClients).FindOne(ctx, bson.M{
			"key": key,
		})
	}

	clientInfo := database.ClientInfo{}
	if err = result.Decode(&clientInfo); err != nil {
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	return &clientInfo, nil
}

// DeactivateClient deactivates the client of the given ID.
func (c *Client) DeactivateClient(ctx context.Context, projectID, clientID types.ID) (*database.ClientInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}
	encodedClientID, err := encodeID(clientID)
	if err != nil {
		return nil, err
	}

	res := c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
		"_id":        encodedClientID,
		"project_id": encodedProjectID,
	}, bson.M{
		"$set": bson.M{
			"status":     database.ClientDeactivated,
			"updated_at": gotime.Now(),
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	clientInfo := database.ClientInfo{}
	if err := res.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", clientID, database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	return &clientInfo, nil
}

// FindClientInfoByID finds the client of the given ID.
func (c *Client) FindClientInfoByID(ctx context.Context, projectID, clientID types.ID) (*database.ClientInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}
	encodedClientID, err := encodeID(clientID)
	if err != nil {
		return nil, err
	}

	result := c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
		"_id":        encodedClientID,
		"project_id": encodedProjectID,
	}, bson.M{
		"$set": bson.M{
			"updated_at": gotime.Now(),
		},
	})

	clientInfo := database.ClientInfo{}
	if err := result.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", clientID, database.ErrClientNotFound)
		}
	}

	return &clientInfo, nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (c *Client) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	encodedClientID, err := encodeID(clientInfo.ID)
	if err != nil {
		return err
	}

	clientDocInfoKey := "documents." + docInfo.ID.String() + "."
	clientDocInfo, ok := clientInfo.Documents[docInfo.ID]
	if !ok {
		return fmt.Errorf("client doc info: %w", database.ErrDocumentNeverAttached)
	}

	updater := bson.M{
		"$max": bson.M{
			clientDocInfoKey + "server_seq": clientDocInfo.ServerSeq,
			clientDocInfoKey + "client_seq": clientDocInfo.ClientSeq,
		},
		"$set": bson.M{
			clientDocInfoKey + "status": clientDocInfo.Status,
			"updated_at":                clientInfo.UpdatedAt,
		},
	}

	attached, err := clientInfo.IsAttached(docInfo.ID)
	if err != nil {
		return err
	}

	if !attached {
		updater = bson.M{
			"$set": bson.M{
				clientDocInfoKey + "server_seq": 0,
				clientDocInfoKey + "client_seq": 0,
				clientDocInfoKey + "status":     clientDocInfo.Status,
				"updated_at":                    clientInfo.UpdatedAt,
			},
		}
	}

	result := c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
		"_id": encodedClientID,
	}, updater)

	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return fmt.Errorf("%s: %w", clientInfo.Key, database.ErrClientNotFound)
		}
		return fmt.Errorf("update client info: %w", result.Err())
	}

	return nil
}

// findDeactivateCandidatesPerProject finds the clients that need housekeeping per project.
func (c *Client) findDeactivateCandidatesPerProject(
	ctx context.Context,
	project *database.ProjectInfo,
	candidatesLimit int,
) ([]*database.ClientInfo, error) {
	encodedProjectID, err := encodeID(project.ID)
	if err != nil {
		return nil, err
	}

	clientDeactivateThreshold, err := project.ClientDeactivateThresholdAsTimeDuration()
	if err != nil {
		return nil, err
	}

	cursor, err := c.collection(colClients).Find(ctx, bson.M{
		"project_id": encodedProjectID,
		"status":     database.ClientActivated,
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

// FindDeactivateCandidates finds the clients that need housekeeping.
func (c *Client) FindDeactivateCandidates(
	ctx context.Context,
	candidatesLimitPerProject int,
	projectFetchSize int,
	lastProjectID types.ID,
) (types.ID, []*database.ClientInfo, error) {
	projects, err := c.listProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []*database.ClientInfo
	for _, project := range projects {
		clientInfos, err := c.findDeactivateCandidatesPerProject(ctx, project, candidatesLimitPerProject)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		candidates = append(candidates, clientInfos...)
	}

	var topProjectID types.ID
	if len(projects) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projects[len(projects)-1].ID
	}
	return topProjectID, candidates, nil
}

// FindDocInfoByKeyAndOwner finds the document of the given key. If the
// createDocIfNotExist condition is true, create the document if it does not
// exist.
func (c *Client) FindDocInfoByKeyAndOwner(
	ctx context.Context,
	projectID types.ID,
	clientID types.ID,
	docKey key.Key,
	createDocIfNotExist bool,
) (*database.DocInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}
	encodedOwnerID, err := encodeID(clientID)
	if err != nil {
		return nil, err
	}

	filter := bson.M{
		"project_id": encodedProjectID,
		"key":        docKey,
		"removed_at": bson.M{
			"$exists": false,
		},
	}
	now := gotime.Now()
	res, err := c.collection(colDocuments).UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{
			"accessed_at": now,
		},
	}, options.Update().SetUpsert(createDocIfNotExist))
	if err != nil {
		return nil, fmt.Errorf("upsert document: %w", err)
	}

	var result *mongo.SingleResult
	if res.UpsertedCount > 0 {
		result = c.collection(colDocuments).FindOneAndUpdate(ctx, bson.M{
			"_id": res.UpsertedID,
		}, bson.M{
			"$set": bson.M{
				"owner":      encodedOwnerID,
				"server_seq": 0,
				"created_at": now,
			},
		})
	} else {
		result = c.collection(colDocuments).FindOne(ctx, filter)
		if result.Err() == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s %s: %w", projectID, docKey, database.ErrDocumentNotFound)
		}
		if result.Err() != nil {
			return nil, fmt.Errorf("find document: %w", result.Err())
		}
	}

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &docInfo, nil
}

// FindDocInfoByKey finds the document of the given key.
func (c *Client) FindDocInfoByKey(
	ctx context.Context,
	projectID types.ID,
	docKey key.Key,
) (*database.DocInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}

	result := c.collection(colDocuments).FindOne(ctx, bson.M{
		"project_id": encodedProjectID,
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

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &docInfo, nil
}

// FindDocInfoByID finds a docInfo of the given ID.
func (c *Client) FindDocInfoByID(
	ctx context.Context,
	projectID types.ID,
	id types.ID,
) (*database.DocInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}

	encodedDocID, err := encodeID(id)
	if err != nil {
		return nil, err
	}

	result := c.collection(colDocuments).FindOne(ctx, bson.M{
		"_id":        encodedDocID,
		"project_id": encodedProjectID,
	})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf("%s: %w", id, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find document: %w", result.Err())
	}

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &docInfo, nil
}

// UpdateDocInfoStatusToRemoved updates the document status to removed.
func (c *Client) UpdateDocInfoStatusToRemoved(
	ctx context.Context,
	projectID types.ID,
	id types.ID,
) error {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return err
	}

	encodedDocID, err := encodeID(id)
	if err != nil {
		return err
	}

	result := c.collection(colDocuments).FindOneAndUpdate(ctx, bson.M{
		"_id":        encodedDocID,
		"project_id": encodedProjectID,
	}, bson.M{
		"$set": bson.M{
			"removed_at": gotime.Now(),
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	if result.Err() == mongo.ErrNoDocuments {
		return fmt.Errorf("%s: %w", id, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		return fmt.Errorf("update document info status to removed: %w", result.Err())
	}

	return nil
}

// CreateChangeInfos stores the given changes and doc info.
func (c *Client) CreateChangeInfos(
	ctx context.Context,
	projectID types.ID,
	docInfo *database.DocInfo,
	initialServerSeq int64,
	changes []*change.Change,
	isRemoved bool,
) error {
	encodedDocID, err := encodeID(docInfo.ID)
	if err != nil {
		return err
	}

	var models []mongo.WriteModel
	for _, cn := range changes {
		encodedOperations, err := database.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}
		encodedPresence, err := database.EncodePresenceChange(cn.PresenceChange())
		if err != nil {
			return err
		}

		models = append(models, mongo.NewUpdateOneModel().SetFilter(bson.M{
			"doc_id":     encodedDocID,
			"server_seq": cn.ServerSeq(),
		}).SetUpdate(bson.M{"$set": bson.M{
			"actor_id":        encodeActorID(cn.ID().ActorID()),
			"client_seq":      cn.ID().ClientSeq(),
			"lamport":         cn.ID().Lamport(),
			"message":         cn.Message(),
			"operations":      encodedOperations,
			"presence_change": encodedPresence,
		}}).SetUpsert(true))
	}

	// TODO(hackerwins): We need to handle the updates for the two collections
	// below atomically.
	if len(changes) > 0 {
		if _, err = c.collection(colChanges).BulkWrite(
			ctx,
			models,
			options.BulkWrite().SetOrdered(true),
		); err != nil {
			return fmt.Errorf("bulk write changes: %w", err)
		}
	}

	now := gotime.Now()
	updateFields := bson.M{
		"server_seq": docInfo.ServerSeq,
		"updated_at": now,
	}
	if isRemoved {
		updateFields["removed_at"] = now
	}

	res, err := c.collection(colDocuments).UpdateOne(ctx, bson.M{
		"_id":        encodedDocID,
		"server_seq": initialServerSeq,
	}, bson.M{
		"$set": updateFields,
	})
	if err != nil {
		return fmt.Errorf("update document: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%s: %w", docInfo.ID, database.ErrConflictOnUpdate)
	}
	if isRemoved {
		docInfo.RemovedAt = now
	}

	return nil
}

// PurgeStaleChanges delete changes before the smallest in `syncedseqs` to
// save storage.
func (c *Client) PurgeStaleChanges(
	ctx context.Context,
	docID types.ID,
) error {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return err
	}

	// Find the smallest server seq in `syncedseqs`.
	// Because offline client can pull changes when it becomes online.
	result := c.collection(colSyncedSeqs).FindOne(
		ctx,
		bson.M{"doc_id": encodedDocID},
		options.FindOne().SetSort(bson.M{"server_seq": 1}),
	)
	if result.Err() == mongo.ErrNoDocuments {
		return nil
	}
	if result.Err() != nil {
		return fmt.Errorf("find syncedseqs: %w", result.Err())
	}
	minSyncedSeqInfo := database.SyncedSeqInfo{}
	if err := result.Decode(&minSyncedSeqInfo); err != nil {
		return fmt.Errorf("decode syncedseq: %w", err)
	}

	// Delete all changes before the smallest server seq.
	if _, err := c.collection(colChanges).DeleteMany(
		ctx,
		bson.M{
			"doc_id":     encodedDocID,
			"server_seq": bson.M{"$lt": minSyncedSeqInfo.ServerSeq},
		},
		options.Delete(),
	); err != nil {
		return fmt.Errorf("delete changes: %w", err)
	}

	return nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (c *Client) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docID types.ID,
	from int64,
	to int64,
) ([]*change.Change, error) {
	infos, err := c.FindChangeInfosBetweenServerSeqs(ctx, docID, from, to)
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
	docID types.ID,
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	cursor, err := c.collection(colChanges).Find(ctx, bson.M{
		"doc_id": encodedDocID,
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
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (c *Client) CreateSnapshotInfo(
	ctx context.Context,
	docID types.ID,
	doc *document.InternalDocument,
) error {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return err
	}
	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return err
	}

	if _, err := c.collection(colSnapshots).InsertOne(ctx, bson.M{
		"doc_id":     encodedDocID,
		"server_seq": doc.Checkpoint().ServerSeq,
		"lamport":    doc.Lamport(),
		"snapshot":   snapshot,
		"created_at": gotime.Now(),
	}); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}

	return nil
}

// FindSnapshotInfoByID returns the snapshot by the given id.
func (c *Client) FindSnapshotInfoByID(
	ctx context.Context,
	id types.ID,
) (*database.SnapshotInfo, error) {
	encodedID, err := encodeID(id)
	if err != nil {
		return nil, err
	}

	result := c.collection(colSnapshots).FindOne(ctx, bson.M{
		"_id": encodedID,
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
	docID types.ID,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	option := options.FindOne().SetSort(bson.M{
		"server_seq": -1,
	})

	if !includeSnapshot {
		option.SetProjection(bson.M{"Snapshot": 0})
	}

	result := c.collection(colSnapshots).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
		"server_seq": bson.M{
			"$lte": serverSeq,
		},
	}, option)

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

// FindMinSyncedSeqInfo finds the minimum synced sequence info.
func (c *Client) FindMinSyncedSeqInfo(
	ctx context.Context,
	docID types.ID,
) (*database.SyncedSeqInfo, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	syncedSeqResult := c.collection(colSyncedSeqs).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
	}, options.FindOne().SetSort(bson.D{
		{Key: "server_seq", Value: 1},
	}))
	if syncedSeqResult.Err() == mongo.ErrNoDocuments {
		syncedSeqInfo := database.SyncedSeqInfo{}
		return &syncedSeqInfo, nil
	}
	if syncedSeqResult.Err() != nil {
		return nil, fmt.Errorf("find synced seq: %w", syncedSeqResult.Err())
	}

	syncedSeqInfo := database.SyncedSeqInfo{}
	if err := syncedSeqResult.Decode(&syncedSeqInfo); err != nil {
		return nil, fmt.Errorf("decode syncedseq: %w", err)
	}

	return &syncedSeqInfo, nil
}

// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
// and returns the min synced ticket.
func (c *Client) UpdateAndFindMinSyncedTicket(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docID types.ID,
	serverSeq int64,
) (*time.Ticket, error) {
	if err := c.UpdateSyncedSeq(ctx, clientInfo, docID, serverSeq); err != nil {
		return nil, err
	}

	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	// 02. find min synced seq of the given document.
	result := c.collection(colSyncedSeqs).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
	}, options.FindOne().SetSort(bson.D{
		{Key: "lamport", Value: 1},
		{Key: "actor_id", Value: 1},
	}))
	if result.Err() == mongo.ErrNoDocuments {
		return time.InitialTicket, nil
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find smallest syncedseq: %w", result.Err())
	}
	syncedSeqInfo := database.SyncedSeqInfo{}
	if err := result.Decode(&syncedSeqInfo); err != nil {
		return nil, fmt.Errorf("decode syncedseq: %w", err)
	}

	if syncedSeqInfo.ServerSeq == change.InitialServerSeq {
		return time.InitialTicket, nil
	}

	actorID, err := time.ActorIDFromHex(syncedSeqInfo.ActorID.String())
	if err != nil {
		return nil, err
	}

	return time.NewTicket(
		syncedSeqInfo.Lamport,
		time.MaxDelimiter,
		actorID,
	), nil
}

// FindDocInfosByPaging returns the docInfos of the given paging.
func (c *Client) FindDocInfosByPaging(
	ctx context.Context,
	projectID types.ID,
	paging types.Paging[types.ID],
) ([]*database.DocInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}

	filter := bson.M{
		"project_id": bson.M{
			"$eq": encodedProjectID,
		},
		"removed_at": bson.M{
			"$exists": false,
		},
	}
	if paging.Offset != "" {
		encodedOffset, err := encodeID(paging.Offset)
		if err != nil {
			return nil, err
		}

		k := "$lt"
		if paging.IsForward {
			k = "$gt"
		}
		filter["_id"] = bson.M{
			k: encodedOffset,
		}
	}

	opts := options.Find().SetLimit(int64(paging.PageSize))
	if paging.IsForward {
		opts = opts.SetSort(map[string]int{"_id": 1})
	} else {
		opts = opts.SetSort(map[string]int{"_id": -1})
	}

	cursor, err := c.collection(colDocuments).Find(ctx, filter, opts)
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
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}

	cursor, err := c.collection(colDocuments).Find(ctx, bson.M{
		"project_id": encodedProjectID,
		"key": bson.M{"$regex": primitive.Regex{
			Pattern: "^" + escapeRegex(query),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("find document infos: %w", err)
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, fmt.Errorf("fetch documents: %w", err)
	}

	limit := pageSize
	if limit > len(infos) {
		limit = len(infos)
	}
	return &types.SearchResult[*database.DocInfo]{
		TotalCount: len(infos),
		Elements:   infos[:limit],
	}, nil
}

// UpdateSyncedSeq updates the syncedSeq of the given client.
func (c *Client) UpdateSyncedSeq(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docID types.ID,
	serverSeq int64,
) error {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return err
	}
	encodedClientID, err := encodeID(clientInfo.ID)
	if err != nil {
		return err
	}

	// 01. update synced seq of the given client.
	isAttached, err := clientInfo.IsAttached(docID)
	if err != nil {
		return err
	}

	if !isAttached {
		if _, err = c.collection(colSyncedSeqs).DeleteOne(ctx, bson.M{
			"doc_id":    encodedDocID,
			"client_id": encodedClientID,
		}, options.Delete()); err != nil {
			return fmt.Errorf("delete synced seq: %w", err)
		}
		return nil
	}

	ticket, err := c.findTicketByServerSeq(ctx, docID, serverSeq)
	if err != nil {
		return err
	}

	// NOTE: skip storing the initial ticket to prevent GC interruption.
	//       Documents in this state do not need to be saved because they do not
	//       have any tombstones to be referenced by other documents.
	//
	//       (The initial ticket is used as the creation time of the root
	//       element that operations can not remove.)
	if ticket.Compare(time.InitialTicket) == 0 {
		return nil
	}

	if _, err = c.collection(colSyncedSeqs).UpdateOne(ctx, bson.M{
		"doc_id":    encodedDocID,
		"client_id": encodedClientID,
	}, bson.M{
		"$set": bson.M{
			"lamport":    ticket.Lamport(),
			"actor_id":   encodeActorID(ticket.ActorID()),
			"server_seq": serverSeq,
		},
	}, options.Update().SetUpsert(true)); err != nil {
		return fmt.Errorf("upsert synced seq: %w", err)
	}

	return nil
}

// IsDocumentAttached returns whether the given document is attached to clients.
func (c *Client) IsDocumentAttached(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
	excludeClientID types.ID,
) (bool, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return false, err
	}

	clientDocInfoKey := "documents." + docID.String() + "."
	filter := bson.M{
		"project_id":                encodedProjectID,
		clientDocInfoKey + "status": database.DocumentAttached,
	}

	if excludeClientID != "" {
		encodedExcludeClientID, err := encodeID(excludeClientID)
		if err != nil {
			return false, err
		}

		filter["_id"] = bson.M{"$ne": encodedExcludeClientID}
	}

	result := c.collection(colClients).FindOne(ctx, filter)
	if result.Err() == mongo.ErrNoDocuments {
		return false, nil
	}

	return true, nil
}

func (c *Client) findTicketByServerSeq(
	ctx context.Context,
	docID types.ID,
	serverSeq int64,
) (*time.Ticket, error) {
	if serverSeq == change.InitialServerSeq {
		return time.InitialTicket, nil
	}

	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	result := c.collection(colChanges).FindOne(ctx, bson.M{
		"doc_id":     encodedDocID,
		"server_seq": serverSeq,
	})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf(
			"change docID=%s serverSeq=%d: %w",
			docID.String(),
			serverSeq,
			database.ErrDocumentNotFound,
		)
	}
	if result.Err() != nil {
		return nil, fmt.Errorf("find change: %w", result.Err())
	}

	changeInfo := database.ChangeInfo{}
	if err := result.Decode(&changeInfo); err != nil {
		return nil, fmt.Errorf("decode change: %w", err)
	}

	actorID, err := time.ActorIDFromHex(changeInfo.ActorID.String())
	if err != nil {
		return nil, err
	}

	return time.NewTicket(
		changeInfo.Lamport,
		time.MaxDelimiter,
		actorID,
	), nil
}

func (c *Client) collection(
	name string,
	opts ...*options.CollectionOptions,
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
