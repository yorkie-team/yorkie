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

const (
	// StatusKey is the key of the status field.
	StatusKey = "status"
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
			SetRegistry(NewRegistryBuilder().Build()),
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

	_, err = c.collection(ColUsers).UpdateOne(ctx, bson.M{
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
			"name":                        candidate.Name,
			"owner":                       candidate.Owner,
			"client_deactivate_threshold": candidate.ClientDeactivateThreshold,
			"public_key":                  candidate.PublicKey,
			"secret_key":                  candidate.SecretKey,
			"created_at":                  candidate.CreatedAt,
		},
	}, options.Update().SetUpsert(true))
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

	info.ID = types.ID(result.InsertedID.(primitive.ObjectID).Hex())
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
func (c *Client) ActivateClient(ctx context.Context, projectID types.ID, key string) (*database.ClientInfo, error) {
	now := gotime.Now()
	res, err := c.collection(ColClients).UpdateOne(ctx, bson.M{
		"project_id": projectID,
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

	return &clientInfo, nil
}

// DeactivateClient deactivates the client of the given refKey and updates document statuses as detached.
func (c *Client) DeactivateClient(ctx context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	res := c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.ClientID,
	}, bson.A{
		bson.M{
			"$set": bson.M{
				"status":     database.ClientDeactivated,
				"updated_at": gotime.Now(),
				"documents": bson.M{
					"$arrayToObject": bson.M{
						"$map": bson.M{
							"input": bson.M{"$objectToArray": "$documents"},
							"as":    "doc",
							"in": bson.M{
								"k": "$$doc.k",
								"v": bson.M{
									"$cond": bson.M{
										"if": bson.M{"$eq": bson.A{"$$doc.v.status", database.DocumentAttached}},
										"then": bson.M{
											"client_seq": 0,
											"server_seq": 0,
											"status":     database.DocumentDetached,
										},
										"else": "$$doc.v",
									},
								},
							},
						},
					},
				},
			},
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	clientInfo := database.ClientInfo{}
	if err := res.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", refKey, database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("decode client info: %w", err)
	}

	return &clientInfo, nil
}

// FindClientInfoByRefKey finds the client of the given refKey.
func (c *Client) FindClientInfoByRefKey(ctx context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
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
	clientDocInfoKey := getClientDocInfoKey(docInfo.ID)
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
			clientDocInfoKey + StatusKey: clientDocInfo.Status,
			"updated_at":                 clientInfo.UpdatedAt,
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
				clientDocInfoKey + StatusKey:    clientDocInfo.Status,
				"updated_at":                    clientInfo.UpdatedAt,
			},
		}
	}

	result := c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
		"project_id": clientInfo.ProjectID,
		"_id":        clientInfo.ID,
	}, updater)

	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return fmt.Errorf("%s: %w", clientInfo.Key, database.ErrClientNotFound)
		}
		return fmt.Errorf("update client info: %w", result.Err())
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

// FindDocInfoByKeyAndOwner finds the document of the given key. If the
// createDocIfNotExist condition is true, create the document if it does not
// exist.
func (c *Client) FindDocInfoByKeyAndOwner(
	ctx context.Context,
	clientRefKey types.ClientRefKey,
	docKey key.Key,
	createDocIfNotExist bool,
) (*database.DocInfo, error) {
	filter := bson.M{
		"project_id": clientRefKey.ProjectID,
		"key":        docKey,
		"removed_at": bson.M{
			"$exists": false,
		},
	}
	now := gotime.Now()
	res, err := c.collection(ColDocuments).UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{
			"accessed_at": now,
		},
	}, options.Update().SetUpsert(createDocIfNotExist))
	if err != nil {
		return nil, fmt.Errorf("upsert document: %w", err)
	}

	var result *mongo.SingleResult
	if res.UpsertedCount > 0 {
		result = c.collection(ColDocuments).FindOneAndUpdate(ctx, bson.M{
			"project_id": clientRefKey.ProjectID,
			"_id":        res.UpsertedID,
		}, bson.M{
			"$set": bson.M{
				"owner":      clientRefKey.ClientID,
				"server_seq": 0,
				"created_at": now,
			},
		})
	} else {
		result = c.collection(ColDocuments).FindOne(ctx, filter)
		if result.Err() == mongo.ErrNoDocuments {
			return nil, fmt.Errorf(
				"%s %s: %w",
				clientRefKey.ProjectID,
				docKey,
				database.ErrDocumentNotFound,
			)
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

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &docInfo, nil
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

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}

	return &docInfo, nil
}

// UpdateDocInfoStatusToRemoved updates the document status to removed.
func (c *Client) UpdateDocInfoStatusToRemoved(
	ctx context.Context,
	refKey types.DocRefKey,
) error {
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

	return nil
}

// CreateChangeInfos stores the given changes and doc info.
func (c *Client) CreateChangeInfos(
	ctx context.Context,
	_ types.ID,
	docInfo *database.DocInfo,
	initialServerSeq int64,
	changes []*change.Change,
	isRemoved bool,
) error {
	docRefKey := docInfo.RefKey()

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
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
			"server_seq": cn.ServerSeq(),
		}).SetUpdate(bson.M{"$set": bson.M{
			"actor_id":        cn.ID().ActorID(),
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
		if _, err := c.collection(ColChanges).BulkWrite(
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
	}

	for _, cn := range changes {
		if len(cn.Operations()) > 0 {
			updateFields["updated_at"] = now
			break
		}
	}

	if isRemoved {
		updateFields["removed_at"] = now
	}

	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"_id":        docRefKey.DocID,
		"server_seq": initialServerSeq,
	}, bson.M{
		"$set": updateFields,
	})
	if err != nil {
		return fmt.Errorf("update document: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%s: %w", docRefKey, database.ErrConflictOnUpdate)
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
	docRefKey types.DocRefKey,
) error {
	// Find the smallest server seq in `syncedseqs`.
	// Because offline client can pull changes when it becomes online.
	result := c.collection(ColSyncedSeqs).FindOne(
		ctx,
		bson.M{
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
		},
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
	if _, err := c.collection(ColChanges).DeleteMany(
		ctx,
		bson.M{
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
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
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
		"server_seq": doc.Checkpoint().ServerSeq,
		"lamport":    doc.Lamport(),
		"snapshot":   snapshot,
		"created_at": gotime.Now(),
	}); err != nil {
		return fmt.Errorf("insert snapshot: %w", err)
	}

	return nil
}

// FindSnapshotInfoByRefKey returns the snapshot by the given refKey.
func (c *Client) FindSnapshotInfoByRefKey(
	ctx context.Context,
	refKey types.SnapshotRefKey,
) (*database.SnapshotInfo, error) {
	result := c.collection(ColSnapshots).FindOne(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"doc_id":     refKey.DocID,
		"server_seq": refKey.ServerSeq,
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
	docRefKey types.DocRefKey,
) (*database.SyncedSeqInfo, error) {
	syncedSeqResult := c.collection(ColSyncedSeqs).FindOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
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
	docRefKey types.DocRefKey,
	serverSeq int64,
) (*time.Ticket, error) {
	if err := c.UpdateSyncedSeq(ctx, clientInfo, docRefKey, serverSeq); err != nil {
		return nil, err
	}

	// 02. find min synced seq of the given document.
	result := c.collection(ColSyncedSeqs).FindOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
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
		"key": bson.M{"$regex": primitive.Regex{
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
	docRefKey types.DocRefKey,
	serverSeq int64,
) error {
	// 01. update synced seq of the given client.
	isAttached, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	if !isAttached {
		if _, err = c.collection(ColSyncedSeqs).DeleteOne(ctx, bson.M{
			"project_id": docRefKey.ProjectID,
			"doc_id":     docRefKey.DocID,
			"client_id":  clientInfo.ID,
		}, options.Delete()); err != nil {
			return fmt.Errorf("delete synced seq: %w", err)
		}
		return nil
	}

	ticket, err := c.findTicketByServerSeq(ctx, docRefKey, serverSeq)
	if err != nil {
		return err
	}

	if _, err = c.collection(ColSyncedSeqs).UpdateOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
		"client_id":  clientInfo.ID,
	}, bson.M{
		"$set": bson.M{
			"lamport":    ticket.Lamport(),
			"actor_id":   ticket.ActorID(),
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
	docRefKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	clientDocInfoKey := getClientDocInfoKey(docRefKey.DocID)
	filter := bson.M{
		"project_id":                docRefKey.ProjectID,
		clientDocInfoKey + "status": database.DocumentAttached,
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

func (c *Client) findTicketByServerSeq(
	ctx context.Context,
	docRefKey types.DocRefKey,
	serverSeq int64,
) (*time.Ticket, error) {
	if serverSeq == change.InitialServerSeq {
		return time.InitialTicket, nil
	}

	result := c.collection(ColChanges).FindOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"doc_id":     docRefKey.DocID,
		"server_seq": serverSeq,
	})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, fmt.Errorf(
			"change %s serverSeq=%d: %w",
			docRefKey,
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

func getClientDocInfoKey(docID types.ID) string {
	return fmt.Sprintf("documents.%s.", docID)
}
