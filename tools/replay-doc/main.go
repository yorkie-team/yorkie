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

// Package main provides a read-only tool that replays a document's persisted
// state (closest snapshot + subsequent changes) exactly as the server does in
// packs.BuildInternalDocForServerSeq, applying changes one-by-one to pinpoint
// the first change whose operation fails to apply (e.g. "not applicable
// datatype" caused by a change referencing a GC'd CRDT node).
//
// It never writes to the database: it uses the raw mongo driver with yorkie's
// BSON registry and does NOT call mongo.Dial (which would ensureIndexes).
//
// Usage:
//
//	go run ./tools/replay-doc \
//	  -uri "mongodb://admin:admin@localhost:27017/?authSource=admin" \
//	  -db yorkie-meta -doc <docID hex> [-to <serverSeq>]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodb "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
	ymongo "github.com/yorkie-team/yorkie/server/backend/database/mongo"
)

func main() {
	uri := flag.String("uri", "mongodb://admin:admin@localhost:27017/?authSource=admin", "mongo connection uri")
	dbName := flag.String("db", "yorkie-meta", "yorkie database name")
	docHex := flag.String("doc", "", "document _id (hex)")
	to := flag.Int64("to", -1, "target server_seq (default: document.server_seq)")
	flag.Parse()

	if *docHex == "" {
		fmt.Fprintln(os.Stderr, "-doc is required")
		os.Exit(2)
	}

	if err := run(*uri, *dbName, *docHex, *to); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(uri, dbName, docHex string, to int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	docID, err := bson.ObjectIDFromHex(docHex)
	if err != nil {
		return fmt.Errorf("parse doc id: %w", err)
	}

	cli, err := mongodb.Connect(options.Client().
		ApplyURI(uri).
		SetRegistry(ymongo.NewRegistryBuilder()))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = cli.Disconnect(ctx) }()
	db := cli.Database(dbName)

	// 0. document metadata
	var doc struct {
		Key       string        `bson:"key"`
		ProjectID bson.ObjectID `bson:"project_id"`
		ServerSeq int64         `bson:"server_seq"`
	}
	if err := db.Collection("documents").FindOne(ctx, bson.M{"_id": docID}).Decode(&doc); err != nil {
		return fmt.Errorf("find document: %w", err)
	}
	if to < 0 {
		to = doc.ServerSeq
	}
	fmt.Printf("doc key=%s server_seq=%d replay target=%d\n", doc.Key, doc.ServerSeq, to)

	// 1. closest snapshot <= target
	var snap database.SnapshotInfo
	err = db.Collection("snapshots").FindOne(ctx,
		bson.M{"doc_id": docID, "server_seq": bson.M{"$lte": to}},
		options.FindOne().SetSort(bson.M{"server_seq": -1}),
	).Decode(&snap)
	if err != nil {
		return fmt.Errorf("find snapshot: %w", err)
	}
	body := snap.Snapshot
	if len(body) == 0 && snap.HasExternalBody {
		var sb database.SnapshotBodyInfo
		if err := db.Collection("snapshot_bodies").FindOne(ctx,
			bson.M{"doc_id": docID, "server_seq": snap.ServerSeq}).Decode(&sb); err != nil {
			return fmt.Errorf("find snapshot body: %w", err)
		}
		body = sb.Snapshot
	}
	if len(body) > 0 {
		body, err = database.DecompressSnapshot(body)
		if err != nil {
			return fmt.Errorf("decompress snapshot: %w", err)
		}
	}
	fmt.Printf("snapshot server_seq=%d lamport=%d bytes=%d\n", snap.ServerSeq, snap.Lamport, len(body))

	idoc, err := document.NewInternalDocumentFromSnapshot(
		key.Key(doc.Key), snap.ServerSeq, snap.Lamport, snap.VersionVector, body,
	)
	if err != nil {
		return fmt.Errorf("build from snapshot: %w", err)
	}
	fmt.Printf("snapshot loaded ok: elements=%d\n", idoc.Root().ElementMapLen())

	// 2. changes (snapshot, target]
	cur, err := db.Collection("changes").Find(ctx,
		bson.M{"doc_id": docID, "server_seq": bson.M{"$gt": snap.ServerSeq, "$lte": to}},
		options.Find().SetSort(bson.M{"server_seq": 1}),
	)
	if err != nil {
		return fmt.Errorf("find changes: %w", err)
	}
	var infos []database.ChangeInfo
	if err := cur.All(ctx, &infos); err != nil {
		return fmt.Errorf("decode changes: %w", err)
	}
	fmt.Printf("replaying %d changes...\n\n", len(infos))

	// 3. apply one-by-one; report the first failure
	lastGood := snap.ServerSeq
	for _, info := range infos {
		c, err := info.ToChange()
		if err != nil {
			fmt.Printf("!! DECODE FAILED server_seq=%d actor=%s lamport=%d: %v\n",
				info.ServerSeq, info.ActorID, info.Lamport, err)
			fmt.Printf("\nlast good server_seq = %d\n", lastGood)
			return nil
		}
		if _, err := idoc.ApplyChanges(c); err != nil {
			fmt.Printf("!! APPLY FAILED server_seq=%d actor=%s lamport=%d msg=%q ops=%d\n",
				info.ServerSeq, info.ActorID, info.Lamport, info.Message, len(info.Operations))
			fmt.Printf("   error: %v\n", err)
			fmt.Printf("\n===> ROLLBACK TARGET: last good server_seq = %d\n", lastGood)
			fmt.Printf("     delete changes with server_seq > %d, set document.server_seq = %d\n", lastGood, lastGood)
			return nil
		}
		lastGood = info.ServerSeq
	}

	fmt.Printf("\nAll %d changes applied cleanly up to server_seq=%d. No corruption found in this range.\n",
		len(infos), lastGood)
	return nil
}
