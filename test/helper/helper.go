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
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/admin"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/sync/etcd"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/rpc"
)

var testStartedAt int64

// Below are the values of the Yorkie config used in the test.
var (
	RPCPort            = 21101
	RPCMaxRequestBytes = uint64(4 * 1024 * 1024)

	ProfilingPort = 21102

	AdminPort = 21103

	HousekeepingInterval            = 1 * gotime.Second
	HousekeepingDeactivateThreshold = 1 * gotime.Minute
	HousekeepingCandidatesLimit     = 10

	SnapshotThreshold          = uint64(10)
	SnapshotWithPurgingChanges = false
	AuthWebhookMaxWaitInterval = 3 * gotime.Millisecond
	AuthWebhookSize            = 100
	AuthWebhookCacheAuthTTL    = 10 * gotime.Second
	AuthWebhookCacheUnauthTTL  = 10 * gotime.Second

	MongoConnectionURI     = "mongodb://localhost:27017"
	MongoConnectionTimeout = "5s"
	MongoPingTimeout       = "5s"

	ETCDEndpoints     = []string{"localhost:2379"}
	ETCDDialTimeout   = 5 * gotime.Second
	ETCDLockLeaseTime = 30 * gotime.Second
)

func init() {
	now := gotime.Now()
	testStartedAt = now.Unix()
}

// TestDBName returns the name of test database with timestamp.
// timestamp is set only once on first call.
func TestDBName() string {
	return fmt.Sprintf("test-%s-%d", server.DefaultMongoYorkieDatabase, testStartedAt)
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
func TestConfig() *server.Config {
	portOffset += 100
	return &server.Config{
		RPC: &rpc.Config{
			Port:            RPCPort + portOffset,
			MaxRequestBytes: RPCMaxRequestBytes,
		},
		Profiling: &profiling.Config{
			Port: ProfilingPort + portOffset,
		},
		Admin: &admin.Config{
			Port: AdminPort + portOffset,
		},
		Housekeeping: &housekeeping.Config{
			Interval:            HousekeepingInterval.String(),
			DeactivateThreshold: HousekeepingDeactivateThreshold.String(),
			CandidatesLimit:     HousekeepingCandidatesLimit,
		},
		Backend: &backend.Config{
			UseDefaultProject:          true,
			SnapshotThreshold:          SnapshotThreshold,
			SnapshotWithPurgingChanges: SnapshotWithPurgingChanges,
			AuthWebhookMaxWaitInterval: AuthWebhookMaxWaitInterval.String(),
			AuthWebhookCacheSize:       AuthWebhookSize,
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

// TestServer returns a new instance of Yorkie for testing.
func TestServer() *server.Yorkie {
	y, err := server.New(TestConfig())
	if err != nil {
		log.Fatal(err)
	}
	return y
}
