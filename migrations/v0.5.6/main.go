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

// Package v056 provides migration for v0.5.6
package v056

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
)

// RunMigration runs migrations for v0.5.6
func RunMigration(ctx context.Context, db *mongo.Client, databaseName string, batchSize int) error {
	err := DetachDocumentsFromDeactivatedClients(ctx, db, databaseName, batchSize)
	if err != nil {
		return err
	}

	return nil
}
