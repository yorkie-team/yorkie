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

package mongo

import (
	"context"
	"fmt"
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
		logging.DefaultLogger().Error(err)
		return nil, err
	}

	pingTimeout := conf.ParsePingTimeout()
	ctxPing, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := client.Ping(ctxPing, readpref.Primary()); err != nil {
		logging.DefaultLogger().Errorf("fail to connect to %s in %f sec", conf.ConnectionURI, pingTimeout.Seconds())
		return nil, err
	}

	if err := ensureIndexes(ctx, client.Database(conf.YorkieDatabase)); err != nil {
		logging.DefaultLogger().Error(err)
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
		logging.DefaultLogger().Error(err)
		return err
	}

	return nil
}

// EnsureDefaultProjectInfo creates the default project info if it does not exist.
func (c *Client) EnsureDefaultProjectInfo(ctx context.Context) (*database.ProjectInfo, error) {
	candidate := database.NewProjectInfo(database.DefaultProjectName)
	candidate.ID = database.DefaultProjectID
	encodedID, err := encodeID(candidate.ID)
	if err != nil {
		return nil, err
	}

	_, err = c.collection(colProjects).UpdateOne(ctx, bson.M{
		"_id": encodedID,
	}, bson.M{
		"$setOnInsert": bson.M{
			"name":       candidate.Name,
			"public_key": candidate.PublicKey,
			"secret_key": candidate.SecretKey,
			"created_at": gotime.Now(),
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		return nil, err
	}

	result := c.collection(colProjects).FindOne(ctx, bson.M{
		"_id": encodedID,
	})

	info := database.ProjectInfo{}
	if err := result.Decode(&info); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("default: %w", database.ErrProjectNotFound)
		}
		return nil, err
	}

	return &info, nil
}

// CreateProjectInfo creates a new project.
func (c *Client) CreateProjectInfo(ctx context.Context, name string) (*database.ProjectInfo, error) {
	info := database.NewProjectInfo(name)
	result, err := c.collection(colProjects).InsertOne(ctx, bson.M{
		"name":       info.Name,
		"public_key": info.PublicKey,
		"secret_key": info.SecretKey,
		"created_at": gotime.Now(),
	})
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, database.ErrProjectAlreadyExists
		}

		logging.From(ctx).Error(err)
		return nil, err
	}

	info.ID = types.ID(result.InsertedID.(primitive.ObjectID).Hex())
	return info, nil
}

// ListProjectInfos returns all project infos.
func (c *Client) ListProjectInfos(ctx context.Context) ([]*database.ProjectInfo, error) {
	cursor, err := c.collection(colProjects).Find(ctx, bson.M{})
	if err != nil {
		logging.From(ctx).Error(err)
		return nil, err
	}

	var infos []*database.ProjectInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, err
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
		return nil, err
	}

	return &projectInfo, nil
}

// FindProjectInfoByName returns a project by name.
func (c *Client) FindProjectInfoByName(ctx context.Context, name string) (*database.ProjectInfo, error) {
	result := c.collection(colProjects).FindOne(ctx, bson.M{
		"name": name,
	})

	projectInfo := database.ProjectInfo{}
	if err := result.Decode(&projectInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", name, database.ErrProjectNotFound)
		}
		return nil, err
	}

	return &projectInfo, nil
}

// UpdateProjectInfo updates the project info.
func (c *Client) UpdateProjectInfo(
	ctx context.Context,
	id types.ID,
	field *database.ProjectField,
) (*database.ProjectInfo, error) {
	encodedID, err := encodeID(id)
	if err != nil {
		return nil, err
	}

	// field.Name unique test
	cursor, err := c.collection(colProjects).Find(ctx, bson.M{
		"name": field.Name,
	})
	if err != nil {
		return nil, err
	}
	if cursor != nil {
		return nil, fmt.Errorf("%s: %w", field.Name, database.ErrProjectAlreadyExists)
	}

	now := gotime.Now()
	res := c.collection(colProjects).FindOneAndUpdate(ctx, bson.M{
		"_id": encodedID,
	}, bson.M{
		"$set": bson.M{
			"name":       field.Name,
			"updated_at": now,
		},
	}, options.FindOneAndUpdate().SetReturnDocument(options.After))

	ProjectInfo := database.ProjectInfo{}
	if err := res.Decode(&ProjectInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
		}
		return nil, err
	}

	return &ProjectInfo, nil
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
		logging.From(ctx).Error(err)
		return nil, err
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
		return nil, err
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
	})

	clientInfo := database.ClientInfo{}
	if err := res.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", clientID, database.ErrClientNotFound)
		}
		return nil, err
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
	clientDocInfoKey := "documents." + docInfo.ID.String() + "."
	clientDocInfo := clientInfo.Documents[docInfo.ID]

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
		"key": clientInfo.Key,
	}, updater)

	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return fmt.Errorf("%s: %w", clientInfo.Key, database.ErrClientNotFound)
		}
		logging.From(ctx).Error(result.Err())
		return result.Err()
	}

	return nil
}

// FindDeactivateCandidates finds the clients that need housekeeping.
func (c *Client) FindDeactivateCandidates(
	ctx context.Context,
	deactivateThreshold gotime.Duration,
	candidatesLimit int,
) ([]*database.ClientInfo, error) {
	cursor, err := c.collection(colClients).Find(ctx, bson.M{
		"status": database.ClientActivated,
		"updated_at": bson.M{
			"$lte": gotime.Now().Add(-deactivateThreshold),
		},
	}, options.Find().SetLimit(int64(candidatesLimit)))

	if err != nil {
		return nil, err
	}

	var clientInfos []*database.ClientInfo
	if err := cursor.All(ctx, &clientInfos); err != nil {
		return nil, err
	}

	return clientInfos, nil
}

// FindDocInfoByKey finds the document of the given key. If the
// createDocIfNotExist condition is true, create the document if it does not
// exist.
func (c *Client) FindDocInfoByKey(
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

	now := gotime.Now()
	res, err := c.collection(colDocuments).UpdateOne(ctx, bson.M{
		"project_id": encodedProjectID,
		"key":        docKey,
	}, bson.M{
		"$set": bson.M{
			"accessed_at": now,
		},
	}, options.Update().SetUpsert(createDocIfNotExist))
	if err != nil {
		logging.From(ctx).Error(err)
		return nil, err
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
		result = c.collection(colDocuments).FindOne(ctx, bson.M{
			"project_id": encodedProjectID,
			"key":        docKey,
		})
		if result.Err() == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s %s: %w", projectID, docKey, database.ErrDocumentNotFound)
		}
		if result.Err() != nil {
			logging.From(ctx).Error(result.Err())
			return nil, result.Err()
		}
	}

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, err
	}

	return &docInfo, nil
}

// FindDocInfoByID finds a docInfo of the given ID.
func (c *Client) FindDocInfoByID(ctx context.Context, id types.ID) (*database.DocInfo, error) {
	encodedDocID, err := encodeID(id)
	if err != nil {
		return nil, err
	}

	result := c.collection(colDocuments).FindOne(ctx, bson.M{
		"_id": encodedDocID,
	})
	if result.Err() == mongo.ErrNoDocuments {
		logging.From(ctx).Error(result.Err())
		return nil, fmt.Errorf("%s: %w", id, database.ErrDocumentNotFound)
	}
	if result.Err() != nil {
		logging.From(ctx).Error(result.Err())
		return nil, result.Err()
	}

	docInfo := database.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, err
	}

	return &docInfo, nil
}

// CreateChangeInfos stores the given changes and doc info.
func (c *Client) CreateChangeInfos(
	ctx context.Context,
	projectID types.ID,
	docInfo *database.DocInfo,
	initialServerSeq uint64,
	changes []*change.Change,
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

		models = append(models, mongo.NewUpdateOneModel().SetFilter(bson.M{
			"doc_id":     encodedDocID,
			"server_seq": cn.ServerSeq(),
		}).SetUpdate(bson.M{"$set": bson.M{
			"actor_id":   encodeActorID(cn.ID().ActorID()),
			"client_seq": cn.ID().ClientSeq(),
			"lamport":    cn.ID().Lamport(),
			"message":    cn.Message(),
			"operations": encodedOperations,
		}}).SetUpsert(true))
	}

	// TODO(hackerwins): We need to handle the updates for the two collections
	// below atomically.
	if _, err = c.collection(colChanges).BulkWrite(
		ctx,
		models,
		options.BulkWrite().SetOrdered(true),
	); err != nil {
		logging.From(ctx).Error(err)
		return err
	}

	res, err := c.collection(colDocuments).UpdateOne(ctx, bson.M{
		"_id":        encodedDocID,
		"server_seq": initialServerSeq,
	}, bson.M{
		"$set": bson.M{
			"server_seq": docInfo.ServerSeq,
			"updated_at": gotime.Now(),
		},
	})
	if err != nil {
		logging.From(ctx).Error(err)
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%s: %w", docInfo.ID, database.ErrConflictOnUpdate)
	}

	return nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (c *Client) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docID types.ID,
	from uint64,
	to uint64,
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
	from uint64,
	to uint64,
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
		logging.From(ctx).Error(err)
		return nil, err
	}

	var infos []*database.ChangeInfo
	if err := cursor.All(ctx, &infos); err != nil {
		logging.From(ctx).Error(cursor.Err())
		return nil, cursor.Err()
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
	snapshot, err := converter.ObjectToBytes(doc.RootObject())
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
		logging.From(ctx).Error(err)
		return err
	}

	return nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (c *Client) FindClosestSnapshotInfo(
	ctx context.Context,
	docID types.ID,
	serverSeq uint64,
) (*database.SnapshotInfo, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	result := c.collection(colSnapshots).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
		"server_seq": bson.M{
			"$lte": serverSeq,
		},
	}, options.FindOne().SetSort(bson.M{
		"server_seq": -1,
	}))

	snapshotInfo := &database.SnapshotInfo{}
	if result.Err() == mongo.ErrNoDocuments {
		return snapshotInfo, nil
	}

	if result.Err() != nil {
		logging.From(ctx).Error(result.Err())
		return nil, result.Err()
	}

	if err := result.Decode(snapshotInfo); err != nil {
		return nil, err
	}

	return snapshotInfo, nil
}

// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
// and returns the min synced ticket.
func (c *Client) UpdateAndFindMinSyncedTicket(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docID types.ID,
	serverSeq uint64,
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
		logging.From(ctx).Error(result.Err())
		return nil, result.Err()
	}
	syncedSeqInfo := database.SyncedSeqInfo{}
	if err := result.Decode(&syncedSeqInfo); err != nil {
		return nil, err
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
	paging types.Paging,
) ([]*database.DocInfo, error) {
	encodedProjectID, err := encodeID(projectID)
	if err != nil {
		return nil, err
	}

	filter := bson.M{
		"project_id": bson.M{
			"$eq": encodedProjectID,
		},
	}
	if paging.PreviousID != "" {
		encodedPreviousID, err := encodeID(paging.PreviousID)
		if err != nil {
			return nil, err
		}

		k := "$lt"
		if paging.IsForward {
			k = "$gt"
		}
		filter["_id"] = bson.M{
			k: encodedPreviousID,
		}
	}

	opts := options.Find().SetLimit(int64(paging.PageSize))
	if !paging.IsForward {
		opts = opts.SetSort(map[string]int{"_id": -1})
	}

	cursor, err := c.collection(colDocuments).Find(ctx, filter, opts)
	if err != nil {
		logging.From(ctx).Error(err)
		return nil, err
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		logging.From(ctx).Error(cursor.Err())
		return nil, cursor.Err()
	}

	return infos, nil
}

// UpdateSyncedSeq updates the syncedSeq of the given client.
func (c *Client) UpdateSyncedSeq(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docID types.ID,
	serverSeq uint64,
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
			logging.From(ctx).Error(err)
			return err
		}
		return nil
	}

	ticket, err := c.findTicketByServerSeq(ctx, docID, serverSeq)
	if err != nil {
		return err
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
		logging.From(ctx).Error(err)
		return err
	}

	return nil
}

func (c *Client) findTicketByServerSeq(
	ctx context.Context,
	docID types.ID,
	serverSeq uint64,
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
		logging.From(ctx).Error(result.Err())
		return nil, result.Err()
	}

	changeInfo := database.ChangeInfo{}
	if err := result.Decode(&changeInfo); err != nil {
		return nil, err
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
