//go:build complex

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package complex

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

type testResult struct {
	flag       bool
	resultDesc string
}

type clientAndDocPair struct {
	cli *client.Client
	doc *document.Document
}

var (
	shardedDBNameForServer   = "test-yorkie-meta-server"
	testRPCServer            *rpc.Server
	testRPCAddr              = fmt.Sprintf("localhost:%d", helper.RPCPort)
	testClient               v1connect.YorkieServiceClient
	testAdminAuthInterceptor *admin.AuthInterceptor
	testAdminClient          v1connect.AdminServiceClient
)

func TestMain(m *testing.M) {
	// Cleanup the previous data in DB
	err := helper.CleanUpAllCollections(shardedDBNameForServer)
	if err != nil {
		log.Fatal(err)
	}

	met, err := prometheus.NewMetrics()
	if err != nil {
		log.Fatal(err)
	}

	be, err := backend.New(&backend.Config{
		AdminUser:                   helper.AdminUser,
		AdminPassword:               helper.AdminPassword,
		UseDefaultProject:           helper.UseDefaultProject,
		ClientDeactivateThreshold:   helper.ClientDeactivateThreshold,
		SnapshotThreshold:           helper.SnapshotThreshold,
		AuthWebhookMaxWaitInterval:  helper.AuthWebhookMaxWaitInterval.String(),
		AuthWebhookMinWaitInterval:  helper.AuthWebhookMinWaitInterval.String(),
		AuthWebhookRequestTimeout:   helper.AuthWebhookRequestTimeout.String(),
		AuthWebhookCacheSize:        helper.AuthWebhookSize,
		AuthWebhookCacheTTL:         helper.AuthWebhookCacheTTL.String(),
		EventWebhookMaxWaitInterval: helper.EventWebhookMaxWaitInterval.String(),
		EventWebhookMinWaitInterval: helper.EventWebhookMinWaitInterval.String(),
		EventWebhookRequestTimeout:  helper.EventWebhookRequestTimeout.String(),
		ProjectCacheSize:            helper.ProjectCacheSize,
		ProjectCacheTTL:             helper.ProjectCacheTTL.String(),
		AdminTokenDuration:          helper.AdminTokenDuration,
		GatewayAddr:                 fmt.Sprintf("localhost:%d", helper.RPCPort),
	}, &mongo.Config{
		ConnectionURI:     helper.MongoConnectionURI,
		YorkieDatabase:    shardedDBNameForServer,
		ConnectionTimeout: helper.MongoConnectionTimeout,
		PingTimeout:       helper.MongoPingTimeout,
	}, &housekeeping.Config{
		Interval:                  helper.HousekeepingInterval.String(),
		CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
		ProjectFetchSize:          helper.HousekeepingProjectFetchSize,
	}, met, nil)
	if err != nil {
		log.Fatal(err)
	}

	project, err := be.DB.FindProjectInfoByID(
		context.Background(),
		database.DefaultProjectID,
	)
	if err != nil {
		log.Fatal(err)
	}

	testRPCServer, err = rpc.NewServer(&rpc.Config{
		Port: helper.RPCPort,
	}, be)
	if err != nil {
		log.Fatal(err)
	}

	if err := testRPCServer.Start(); err != nil {
		log.Fatalf("failed rpc listen: %s\n", err)
	}

	authInterceptor := client.NewAuthInterceptor(project.PublicKey, "")

	conn := http.DefaultClient
	testClient = v1connect.NewYorkieServiceClient(
		conn,
		"http://"+testRPCAddr,
		connect.WithInterceptors(authInterceptor),
	)

	testAdminAuthInterceptor = admin.NewAuthInterceptor("")

	adminConn := http.DefaultClient
	testAdminClient = v1connect.NewAdminServiceClient(
		adminConn,
		"http://"+testRPCAddr,
		connect.WithInterceptors(testAdminAuthInterceptor),
	)

	code := m.Run()

	if err := be.Shutdown(); err != nil {
		log.Fatal(err)
	}
	testRPCServer.Shutdown(true)
	os.Exit(code)
}

func syncClientsThenCheckEqual(t *testing.T, pairs []clientAndDocPair) bool {
	assert.True(t, len(pairs) > 1)
	ctx := context.Background()
	// Save own changes and get previous changes.
	for i, pair := range pairs {
		fmt.Printf("before d%d: %s\n", i+1, pair.doc.Marshal())
		err := pair.cli.Sync(ctx)
		assert.NoError(t, err)
	}

	// Get last client changes.
	// Last client get all precede changes in above loop.
	for _, pair := range pairs[:len(pairs)-1] {
		err := pair.cli.Sync(ctx)
		assert.NoError(t, err)
	}

	// Assert start.
	expected := pairs[0].doc.Marshal()
	fmt.Printf("after d1: %s\n", expected)
	for i, pair := range pairs[1:] {
		v := pair.doc.Marshal()
		fmt.Printf("after d%d: %s\n", i+2, v)
		if expected != v {
			return false
		}
	}

	return true
}

// activeClients creates and activates the given number of clients.
func activeClients(t *testing.T, n int) (clients []*client.Client) {
	for i := 0; i < n; i++ {
		c, err := client.Dial(testRPCAddr)
		assert.NoError(t, err)
		assert.NoError(t, c.Activate(context.Background()))

		clients = append(clients, c)
	}
	return
}

// deactivateAndCloseClients deactivates and closes the given clients.
func deactivateAndCloseClients(t *testing.T, clients []*client.Client) {
	for _, c := range clients {
		assert.NoError(t, c.Deactivate(context.Background()))
		assert.NoError(t, c.Close())
	}
}
