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

// Package helper provides helper functions for testing.
package helper

import (
	"context"
	"fmt"
	"log"
	"strings"
	gotime "time"

	"github.com/stretchr/testify/assert"

	adminClient "github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	key "github.com/yorkie-team/yorkie/pkg/document/key"
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

	AdminUser                             = server.DefaultAdminUser
	AdminPassword                         = server.DefaultAdminPassword
	HousekeepingInterval                  = 1 * gotime.Second
	HousekeepingCandidatesLimitPerProject = 10

	ClientDeactivateThreshold  = "10s"
	SnapshotThreshold          = int64(10)
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

	// documentKeyCounter is used to generate a unique document key.
	documentKeyCounter = 0
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

// CreateAdminCli returns a new instance of admin cli for testing.
func CreateAdminCli(t assert.TestingT, adminAddr string) *adminClient.Client {
	adminCli, err := adminClient.Dial(adminAddr)
	assert.NoError(t, err)

	_, err = adminCli.LogIn(context.Background(), server.DefaultAdminUser, server.DefaultAdminPassword)
	assert.NoError(t, err)

	return adminCli
}

// TestRoot returns the root
func TestRoot() *crdt.Root {
	return crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
}

// TextChangeContext returns the context of test change.
func TextChangeContext(root *crdt.Root) *change.Context {
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
			Interval:                  HousekeepingInterval.String(),
			CandidatesLimitPerProject: HousekeepingCandidatesLimitPerProject,
		},
		Backend: &backend.Config{
			AdminUser:                  server.DefaultAdminUser,
			AdminPassword:              server.DefaultAdminPassword,
			SecretKey:                  server.DefaultSecretKey,
			AdminTokenDuration:         server.DefaultAdminTokenDuration.String(),
			UseDefaultProject:          true,
			ClientDeactivateThreshold:  server.DefaultClientDeactivateThreshold,
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

// TestDocumentKey returns a new instance of document key for testing.
func TestDocumentKey(t interface{ Name() string }) string {
	name := t.Name()
	if err := key.Key(name).Validate(); err == nil {
		return name
	}

	if len(name) > 30 {
		name = name[:30]
	}

	maxLength := len(name)

	if maxLength > 29 {
		maxLength = 29
	}

	// add unique key string to the start of the name
	name = fmt.Sprintf("%d%s", documentKeyCounter%10, name[:maxLength])

	documentKeyCounter++

	sb := strings.Builder{}
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			sb.WriteRune(c + ('a' - 'A'))
		} else if c >= 'a' && c <= 'z' {
			sb.WriteRune(c)
		} else if c >= '0' && c <= '9' {
			sb.WriteRune(c)
		} else {
			sb.WriteRune('-')
		}
	}

	return sb.String()
}
