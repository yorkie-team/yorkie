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

package helper

import (
	"fmt"
	"log"
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db/mongo"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
	"github.com/yorkie-team/yorkie/yorkie/profiling"
	"github.com/yorkie-team/yorkie/yorkie/rpc"
)

var testStartedAt int64

// Below are the values of the Yorkie config used in the test.
const (
	RPCPort                    = 21101
	ProfilingPort              = 21102
	MongoConnectionURI         = "mongodb://localhost:27017"
	MongoConnectionTimeout     = "5s"
	MongoPingTimeout           = "5s"
	SnapshotThreshold          = 10
	Collection                 = "test-collection"
	AuthWebhookMaxWaitInterval = 3 * gotime.Millisecond
	AuthWebhookCacheAuthTTL    = 10 * gotime.Second
	AuthWebhookCacheUnauthTTL  = 10 * gotime.Second
	ETCDDialTimeout            = 5 * gotime.Second
	ETCDLockLeaseTime          = 30 * gotime.Second
)

var (
	// ETCDEndpoints is the list of endpoints to connect to for etcd.
	ETCDEndpoints = []string{"localhost:2379"}
)

func init() {
	now := gotime.Now()
	testStartedAt = now.Unix()
}

// TestDBName returns the name of test database with timestamp.
// timestamp is set only once on first call.
func TestDBName() string {
	return fmt.Sprintf("test-%s-%d", yorkie.DefaultMongoYorkieDatabase, testStartedAt)
}

// TestRoot returns the root
func TestRoot() *json.Root {
	return json.NewRoot(json.NewObject(json.NewRHTPriorityQueueMap(), time.InitialTicket))
}

// TextChangeContext returns the context of test change.
func TextChangeContext(root *json.Root) *change.Context {
	return change.NewContext(
		change.InitialID,
		"",
		root,
	)
}

var portOffset = 0

// TestConfig returns config for creating Yorkie instance.
func TestConfig(authWebhook string) *yorkie.Config {
	portOffset += 100
	return &yorkie.Config{
		RPC: &rpc.Config{
			Port: RPCPort + portOffset,
		},
		Profiling: &profiling.Config{
			Port: ProfilingPort + portOffset,
		},
		Backend: &backend.Config{
			SnapshotThreshold:          SnapshotThreshold,
			AuthWebhookURL:             authWebhook,
			AuthWebhookMaxWaitInterval: AuthWebhookMaxWaitInterval.String(),
			AuthWebhookCacheAuthTTL:    AuthWebhookCacheAuthTTL.String(),
			AuthWebhookCacheUnauthTTL:  AuthWebhookCacheUnauthTTL.String(),
		},
		Mongo: &mongo.Config{
			ConnectionURI:     MongoConnectionURI,
			ConnectionTimeout: MongoConnectionTimeout,
			PingTimeout:       MongoPingTimeout,
			YorkieDatabase:    TestDBName(),
		},
		ETCD: &etcd.Config{
			Endpoints:     ETCDEndpoints,
			DialTimeout:   ETCDDialTimeout.String(),
			LockLeaseTime: ETCDLockLeaseTime.String(),
		},
	}
}

// TestYorkie returns a new instance of Yorkie for testing.
func TestYorkie() *yorkie.Yorkie {
	y, err := yorkie.New(TestConfig(""))
	if err != nil {
		log.Fatal(err)
	}
	return y
}
