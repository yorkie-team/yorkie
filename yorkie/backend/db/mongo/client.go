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
	gotime "time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

// Config is the configuration for creating a Client instance.
type Config struct {
	ConnectionTimeoutSec gotime.Duration `json:"ConnectionTimeoutSec"`
	ConnectionURI        string          `json:"ConnectionURI"`
	YorkieDatabase       string          `json:"YorkieDatabase"`
	PingTimeoutSec       gotime.Duration `json:"PingTimeoutSec"`
}

// Client is a client that connects to Mongo DB and reads or saves Yorkie data.
type Client struct {
	config *Config
	client *mongo.Client
}

// Dial creates an instance of Client and dials the given MongoDB.
func Dial(conf *Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		conf.ConnectionTimeoutSec*gotime.Second,
	)
	defer cancel()

	client, err := mongo.Connect(
		ctx,
		options.Client().ApplyURI(conf.ConnectionURI),
	)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	ctxPing, cancel := context.WithTimeout(ctx, conf.PingTimeoutSec*gotime.Second)
	defer cancel()

	if err := client.Ping(ctxPing, readpref.Primary()); err != nil {
		return nil, errors.Wrapf(err, "fail to connect to %s in %d sec", conf.ConnectionURI, conf.PingTimeoutSec)
	}

	if err := ensureIndexes(ctx, client.Database(conf.YorkieDatabase)); err != nil {
		return nil, err
	}

	log.Logger.Infof("MongoDB connected, URI: %s, DB: %s", conf.ConnectionURI,
		conf.YorkieDatabase)

	return &Client{
		config: conf,
		client: client,
	}, nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	if err := c.client.Disconnect(context.Background()); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// ActivateClient activates the client of the given key.
func (c *Client) ActivateClient(ctx context.Context, key string) (*db.ClientInfo, error) {
	clientInfo := db.ClientInfo{}

	now := gotime.Now()
	res, err := c.collection(ColClients).UpdateOne(ctx, bson.M{
		"key": key,
	}, bson.M{
		"$set": bson.M{
			"status":     db.ClientActivated,
			"updated_at": now,
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var result *mongo.SingleResult
	if res.UpsertedCount > 0 {
		result = c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
			"_id": res.UpsertedID,
		}, bson.M{
			"$set": bson.M{
				"created_at": now,
			},
		})
	} else {
		result = c.collection(ColClients).FindOne(ctx, bson.M{
			"key": key,
		})
	}

	if err = decodeClientInfo(result, &clientInfo); err != nil {
		return nil, err
	}

	return &clientInfo, nil
}

// DeactivateClient deactivates the client of the given ID.
func (c *Client) DeactivateClient(ctx context.Context, clientID db.ID) (*db.ClientInfo, error) {
	encodedClientID, err := encodeID(clientID)
	if err != nil {
		return nil, err
	}

	clientInfo := db.ClientInfo{}
	res := c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
		"_id": encodedClientID,
	}, bson.M{
		"$set": bson.M{
			"status":     db.ClientDeactivated,
			"updated_at": gotime.Now(),
		},
	})

	if err := decodeClientInfo(res, &clientInfo); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.Wrapf(db.ErrClientNotFound, "clientID: %s", clientID)
		}
		return nil, err
	}

	return &clientInfo, nil
}

// FindClientInfoByID finds the client of the given ID.
func (c *Client) FindClientInfoByID(ctx context.Context, clientID db.ID) (*db.ClientInfo, error) {
	encodedClientID, err := encodeID(clientID)
	if err != nil {
		return nil, err
	}

	clientInfo := db.ClientInfo{}
	result := c.collection(ColClients).FindOne(ctx, bson.M{
		"_id": encodedClientID,
	})
	if err := decodeClientInfo(result, &clientInfo); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.Wrapf(db.ErrClientNotFound, "clientID: %s", clientID)
		}
	}

	return &clientInfo, nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (c *Client) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
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

	result := c.collection(ColClients).FindOneAndUpdate(ctx, bson.M{
		"key": clientInfo.Key,
	}, updater)

	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return errors.Wrapf(db.ErrClientNotFound, "clientKey: %s", clientInfo.Key)
		}
		return result.Err()
	}

	return nil
}

// FindDocInfoByKey finds the document of the given key. If the
// createDocIfNotExist condition is true, create the document if it does not
// exist.
func (c *Client) FindDocInfoByKey(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	bsonDocKey string,
	createDocIfNotExist bool,
) (*db.DocInfo, error) {
	encodedOwnerID, err := encodeID(clientInfo.ID)
	if err != nil {
		return nil, err
	}

	docInfo := db.DocInfo{}
	now := gotime.Now()
	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"key": bsonDocKey,
	}, bson.M{
		"$set": bson.M{
			"accessed_at": now,
		},
	}, options.Update().SetUpsert(createDocIfNotExist))
	if err != nil {
		return nil, errors.WithStack(err)
	}

	var result *mongo.SingleResult
	if res.UpsertedCount > 0 {
		result = c.collection(ColDocuments).FindOneAndUpdate(ctx, bson.M{
			"_id": res.UpsertedID,
		}, bson.M{
			"$set": bson.M{
				"owner":      encodedOwnerID,
				"server_seq": 0,
				"created_at": now,
			},
		})
	} else {
		result = c.collection(ColDocuments).FindOne(ctx, bson.M{
			"key": bsonDocKey,
		})
		if result.Err() == mongo.ErrNoDocuments {
			return nil, errors.Wrapf(db.ErrDocumentNotFound, "bson document key: %s", bsonDocKey)
		}
		if result.Err() != nil {
			return nil, errors.WithStack(result.Err())
		}
	}
	if err := decodeDocInfo(result, &docInfo); err != nil {
		return nil, err
	}

	return &docInfo, nil
}

// StoreChangeInfos stores the given changes and doc info.
func (c *Client) StoreChangeInfos(
	ctx context.Context,
	docInfo *db.DocInfo,
	initialServerSeq uint64,
	changes []*change.Change,
) error {
	encodedDocID, err := encodeID(docInfo.ID)
	if err != nil {
		return err
	}

	var models []mongo.WriteModel
	for _, cn := range changes {
		encodedOperations, err := db.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}

		models = append(models, mongo.NewUpdateOneModel().SetFilter(bson.M{
			"doc_id":     encodedDocID,
			"server_seq": cn.ServerSeq(),
		}).SetUpdate(bson.M{"$set": bson.M{
			"actor":      encodeActorID(cn.ID().Actor()),
			"client_seq": cn.ID().ClientSeq(),
			"lamport":    cn.ID().Lamport(),
			"message":    cn.Message(),
			"operations": encodedOperations,
		}}).SetUpsert(true))
	}

	// TODO(hackerwins): We need to handle the updates for the two collections
	// below atomically.
	if _, err = c.collection(ColChanges).BulkWrite(
		ctx,
		models,
		options.BulkWrite().SetOrdered(true),
	); err != nil {
		return errors.WithStack(err)
	}

	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"_id":        encodedDocID,
		"server_seq": initialServerSeq,
	}, bson.M{
		"$set": bson.M{
			"server_seq": docInfo.ServerSeq,
			"updated_at": gotime.Now(),
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}
	if res.MatchedCount == 0 {
		return errors.Wrapf(db.ErrConflictOnUpdate, "docID: %s", docInfo.ID)
	}

	return nil
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (c *Client) CreateSnapshotInfo(
	ctx context.Context,
	docID db.ID,
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

	if _, err := c.collection(ColSnapshots).InsertOne(ctx, bson.M{
		"doc_id":     encodedDocID,
		"server_seq": doc.Checkpoint().ServerSeq,
		"snapshot":   snapshot,
		"created_at": gotime.Now(),
	}); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// FindChangeInfosBetweenServerSeqs returns the changes between two server sequences.
func (c *Client) FindChangeInfosBetweenServerSeqs(
	ctx context.Context,
	docID db.ID,
	from uint64,
	to uint64,
) ([]*change.Change, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	var changes []*change.Change
	cursor, err := c.collection(ColChanges).Find(ctx, bson.M{
		"doc_id": encodedDocID,
		"server_seq": bson.M{
			"$gte": from,
			"$lte": to,
		},
	}, options.Find())
	if err != nil {
		return nil, errors.WithStack(err)
	}

	defer func() {
		if err := cursor.Close(ctx); err != nil {
			log.Logger.Error(err)
		}
	}()

	for cursor.Next(ctx) {
		var changeInfo db.ChangeInfo
		if err := decodeChangeInfo(cursor, &changeInfo); err != nil {
			return nil, err
		}
		c, err := changeInfo.ToChange()
		if err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}

	if cursor.Err() != nil {
		return nil, errors.WithStack(cursor.Err())
	}

	return changes, nil
}

// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
// and returns the min synced ticket.
func (c *Client) UpdateAndFindMinSyncedTicket(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docID db.ID,
	serverSeq uint64,
) (*time.Ticket, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}
	encodedClientID, err := encodeID(clientInfo.ID)
	if err != nil {
		return nil, err
	}

	// 01. update synced seq of the given client.
	isAttached, err := clientInfo.IsAttached(docID)
	if err != nil {
		return nil, err
	}

	if isAttached {
		if _, err = c.collection(ColSyncedSeqs).UpdateOne(ctx, bson.M{
			"doc_id":    encodedDocID,
			"client_id": encodedClientID,
		}, bson.M{
			"$set": bson.M{
				"server_seq": serverSeq,
			},
		}, options.Update().SetUpsert(true)); err != nil {
			return nil, errors.WithStack(err)
		}
	} else {
		if _, err = c.collection(ColSyncedSeqs).DeleteOne(ctx, bson.M{
			"doc_id":    encodedDocID,
			"client_id": encodedClientID,
		}, options.Delete()); err != nil {
			return nil, errors.WithStack(err)
		}
	}

	// 02. find min synced seq of the given document.
	syncedSeqInfo := db.SyncedSeqInfo{}
	result := c.collection(ColSyncedSeqs).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
	}, options.FindOne().SetSort(bson.M{
		"server_seq": 1,
	}))
	if result.Err() == mongo.ErrNoDocuments {
		return time.InitialTicket, nil
	}
	if result.Err() != nil {
		return nil, errors.WithStack(result.Err())
	}
	if err := decodeSyncedSeqInfo(result, &syncedSeqInfo); err != nil {
		return nil, err
	}

	if syncedSeqInfo.ServerSeq == 0 {
		return time.InitialTicket, nil
	}

	// 03. find ticket by seq.
	// TODO: We need to find a way to not access `changes` collection.
	ticket, err := c.findTicketByServerSeq(ctx, docID, syncedSeqInfo.ServerSeq)
	if err != nil {
		return nil, err
	}

	return ticket, nil
}

// FindLastSnapshotInfo finds the last snapshot of the given document.
func (c *Client) FindLastSnapshotInfo(
	ctx context.Context,
	docID db.ID,
) (*db.SnapshotInfo, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	snapshotInfo := &db.SnapshotInfo{}
	result := c.collection(ColSnapshots).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
	}, options.FindOne().SetSort(bson.M{
		"server_seq": -1,
	}))

	if result.Err() == mongo.ErrNoDocuments {
		return snapshotInfo, nil
	}

	if result.Err() != nil {
		return nil, errors.WithStack(result.Err())
	}

	if err := decodeSnapshotInfo(result, snapshotInfo); err != nil {
		return nil, err
	}

	return snapshotInfo, nil
}

func (c *Client) findTicketByServerSeq(
	ctx context.Context,
	docID db.ID,
	serverSeq uint64,
) (*time.Ticket, error) {
	encodedDocID, err := encodeID(docID)
	if err != nil {
		return nil, err
	}

	changeInfo := db.ChangeInfo{}
	result := c.collection(ColChanges).FindOne(ctx, bson.M{
		"doc_id":     encodedDocID,
		"server_seq": serverSeq,
	})
	if result.Err() == mongo.ErrNoDocuments {
		return nil, errors.Wrapf(db.ErrDocumentNotFound, "docID: %s", docID.String())
	}

	if result.Err() != nil {
		return nil, errors.WithStack(result.Err())
	}

	if err := decodeChangeInfo(result, &changeInfo); err != nil {
		return nil, err
	}

	actorID, err := time.ActorIDFromHex(changeInfo.Actor.String())
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
