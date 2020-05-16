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

package testhelper

import (
	"fmt"
	"log"
	"time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	logicalTime "github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

var testStartedAt int64

const (
	RPCPort = 1101

	MongoConnectionURI        = "mongodb://localhost:27017"
	MongoConnectionTimeoutSec = 5
	MongoPingTimeoutSec       = 5

	SnapshotThreshold = 10

	Collection = "test-collection"
)

func init() {
	now := time.Now()
	testStartedAt = now.Unix()
}

// TestDBName returns the name of test database with timestamp.
// timestamp is set only once on first call.
func TestDBName() string {
	return fmt.Sprintf("test-%s-%d", yorkie.DefaultMongoYorkieDatabase, testStartedAt)
}

// TextChangeContext returns the context of test change.
func TextChangeContext() *change.Context {
	return change.NewContext(
		change.InitialID,
		"",
		json.NewRoot(json.NewObject(json.NewRHTPriorityQueueMap(), logicalTime.InitialTicket)),
	)
}

// TestYorkie is return Yorkie instance for testing.
func TestYorkie() *yorkie.Yorkie {
	y, err := yorkie.New(&yorkie.Config{
		RPC: &rpc.Config{
			Port: RPCPort,
		},
		Backend: &backend.Config{
			SnapshotThreshold: SnapshotThreshold,
		},
		Mongo: &mongo.Config{
			ConnectionURI:        MongoConnectionURI,
			ConnectionTimeoutSec: MongoConnectionTimeoutSec,
			PingTimeoutSec:       MongoPingTimeoutSec,
			YorkieDatabase:       TestDBName(),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	return y
}
