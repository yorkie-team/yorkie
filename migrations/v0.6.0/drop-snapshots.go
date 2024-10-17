/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package v060

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"log"
)

func validateDropSnapshot(ctx context.Context, db *mongo.Client, databaseName string) error {
	collections, err := db.Database(databaseName).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		log.Fatal(err)
	}

	collectionExists := false
	for _, collection := range collections {
		if collection == "snapshots" {
			collectionExists = true
			break
		}
	}

	if collectionExists {
		return fmt.Errorf("collection snapshots still exists")
	}

	return nil
}

// DropSnapshots runs migrations for drop snapshots collection
func DropSnapshots(ctx context.Context, db *mongo.Client, databaseName string) error {
	collection := db.Database(databaseName).Collection("snapshots")

	if err := collection.Drop(ctx); err != nil {
		return fmt.Errorf("drop collection: %w", err)
	}
	if err := validateDropSnapshot(ctx, db, databaseName); err != nil {
		return err
	}

	fmt.Println("drop snapshots completed")

	return nil
}
