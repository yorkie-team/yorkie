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
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/log"
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
		log.Logger.Error(err)
		return nil, err
	}

	pingTimeout := conf.ParsePingTimeout()
	ctxPing, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := client.Ping(ctxPing, readpref.Primary()); err != nil {
		log.Logger.Errorf("fail to connect to %s in %f sec", conf.ConnectionURI, pingTimeout.Seconds())
		return nil, err
	}

	if err := ensureIndexes(ctx, client.Database(conf.YorkieDatabase)); err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	log.Logger.Infof("MongoDB connected, URI: %s, DB: %s", conf.ConnectionURI, conf.YorkieDatabase)

	return &Client{
		config: conf,
		client: client,
	}, nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	if err := c.client.Disconnect(context.Background()); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}

// ActivateClient activates the client of the given key.
func (c *Client) ActivateClient(ctx context.Context, key string) (*db.ClientInfo, error) {
	now := gotime.Now()
	res, err := c.collection(colClients).UpdateOne(ctx, bson.M{
		"key": key,
	}, bson.M{
		"$set": bson.M{
			"status":     db.ClientActivated,
			"updated_at": now,
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		log.Logger.Error(err)
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

	clientInfo := db.ClientInfo{}
	if err = result.Decode(&clientInfo); err != nil {
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

	res := c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
		"_id": encodedClientID,
	}, bson.M{
		"$set": bson.M{
			"status":     db.ClientDeactivated,
			"updated_at": gotime.Now(),
		},
	})

	clientInfo := db.ClientInfo{}
	if err := res.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", clientID, db.ErrClientNotFound)
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

	result := c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
		"_id": encodedClientID,
	}, bson.M{
		"$set": bson.M{
			"updated_at": gotime.Now(),
		},
	})

	clientInfo := db.ClientInfo{}
	if err := result.Decode(&clientInfo); err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("%s: %w", clientID, db.ErrClientNotFound)
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

	result := c.collection(colClients).FindOneAndUpdate(ctx, bson.M{
		"key": clientInfo.Key,
	}, updater)

	if result.Err() != nil {
		if result.Err() == mongo.ErrNoDocuments {
			return fmt.Errorf("%s: %w", clientInfo.Key, db.ErrClientNotFound)
		}
		log.Logger.Error(result.Err())
		return result.Err()
	}

	return nil
}

// FindDeactivateCandidates finds the clients that need housekeeping.
func (c *Client) FindDeactivateCandidates(
	ctx context.Context,
	deactivateThreshold gotime.Duration,
	candidatesLimit int,
) ([]*db.ClientInfo, error) {
	cursor, err := c.collection(colClients).Find(ctx, bson.M{
		"status": db.ClientActivated,
		"updated_at": bson.M{
			"$lte": gotime.Now().Add(-deactivateThreshold),
		},
	}, options.Find().SetLimit(int64(candidatesLimit)))

	if err != nil {
		return nil, err
	}

	var clientInfos []*db.ClientInfo
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
	clientInfo *db.ClientInfo,
	bsonDocKey string,
	createDocIfNotExist bool,
) (*db.DocInfo, error) {
	encodedOwnerID, err := encodeID(clientInfo.ID)
	if err != nil {
		return nil, err
	}

	now := gotime.Now()
	res, err := c.collection(colDocuments).UpdateOne(ctx, bson.M{
		"key": bsonDocKey,
	}, bson.M{
		"$set": bson.M{
			"accessed_at": now,
		},
	}, options.Update().SetUpsert(createDocIfNotExist))
	if err != nil {
		log.Logger.Error(err)
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
			"key": bsonDocKey,
		})
		if result.Err() == mongo.ErrNoDocuments {
			log.Logger.Error(result.Err())
			return nil, fmt.Errorf("%s: %w", bsonDocKey, db.ErrDocumentNotFound)
		}
		if result.Err() != nil {
			log.Logger.Error(result.Err())
			return nil, result.Err()
		}
	}

	docInfo := db.DocInfo{}
	if err := result.Decode(&docInfo); err != nil {
		return nil, err
	}

	return &docInfo, nil
}

// CreateChangeInfos stores the given changes and doc info.
func (c *Client) CreateChangeInfos(
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
			"server_seq": *cn.ServerSeq(),
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
		log.Logger.Error(err)
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
		log.Logger.Error(err)
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%s: %w", docInfo.ID, db.ErrConflictOnUpdate)
	}

	return nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (c *Client) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docID db.ID,
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
	docID db.ID,
	from uint64,
	to uint64,
) ([]*db.ChangeInfo, error) {
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
		log.Logger.Error(err)
		return nil, err
	}

	var infos []*db.ChangeInfo
	if err := cursor.All(ctx, &infos); err != nil {
		log.Logger.Error(cursor.Err())
		return nil, cursor.Err()
	}

	return infos, nil
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

	if _, err := c.collection(colSnapshots).InsertOne(ctx, bson.M{
		"doc_id":     encodedDocID,
		"server_seq": doc.Checkpoint().ServerSeq,
		"snapshot":   snapshot,
		"created_at": gotime.Now(),
	}); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
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

	result := c.collection(colSnapshots).FindOne(ctx, bson.M{
		"doc_id": encodedDocID,
	}, options.FindOne().SetSort(bson.M{
		"server_seq": -1,
	}))

	snapshotInfo := &db.SnapshotInfo{}
	if result.Err() == mongo.ErrNoDocuments {
		return snapshotInfo, nil
	}

	if result.Err() != nil {
		log.Logger.Error(result.Err())
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
	clientInfo *db.ClientInfo,
	docID db.ID,
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
		log.Logger.Error(result.Err())
		return nil, result.Err()
	}
	syncedSeqInfo := db.SyncedSeqInfo{}
	if err := result.Decode(&syncedSeqInfo); err != nil {
		return nil, err
	}

	if syncedSeqInfo.ServerSeq == 0 {
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

// UpdateSyncedSeq updates the syncedSeq of the given client.
func (c *Client) UpdateSyncedSeq(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docID db.ID,
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
			log.Logger.Error(err)
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
		log.Logger.Error(err)
		return err
	}

	return nil
}

func (c *Client) findTicketByServerSeq(
	ctx context.Context,
	docID db.ID,
	serverSeq uint64,
) (*time.Ticket, error) {
	if serverSeq == 0 {
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
			db.ErrDocumentNotFound,
		)
	}

	if result.Err() != nil {
		log.Logger.Error(result.Err())
		return nil, result.Err()
	}

	changeInfo := db.ChangeInfo{}
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
