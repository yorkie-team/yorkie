//go:build sharding

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

package sharding

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/yorkie-team/yorkie/admin"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/server/rpc/testcases"
	"github.com/yorkie-team/yorkie/test/helper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	shardedDBNameForServer   = "test-yorkie-meta-server"
	testRPCServer            *rpc.Server
	testRPCAddr              = fmt.Sprintf("localhost:%d", helper.RPCPort)
	testClient               api.YorkieServiceClient
	testAdminAuthInterceptor *admin.AuthInterceptor
	testAdminClient          api.AdminServiceClient
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
		AdminUser:                 helper.AdminUser,
		AdminPassword:             helper.AdminPassword,
		ClientDeactivateThreshold: helper.ClientDeactivateThreshold,
		SnapshotThreshold:         helper.SnapshotThreshold,
		AuthWebhookCacheSize:      helper.AuthWebhookSize,
		ProjectInfoCacheSize:      helper.ProjectInfoCacheSize,
		ProjectInfoCacheTTL:       helper.ProjectInfoCacheTTL.String(),
		AdminTokenDuration:        helper.AdminTokenDuration,
	}, &mongo.Config{
		ConnectionURI:     helper.MongoConnectionURI,
		YorkieDatabase:    shardedDBNameForServer,
		ConnectionTimeout: helper.MongoConnectionTimeout,
		PingTimeout:       helper.MongoPingTimeout,
	}, &housekeeping.Config{
		Interval:                  helper.HousekeepingInterval.String(),
		CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
		ProjectFetchSize:          helper.HousekeepingProjectFetchSize,
	}, met)
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
		Port:                  helper.RPCPort,
		MaxRequestBytes:       helper.RPCMaxRequestBytes,
		MaxConnectionAge:      helper.RPCMaxConnectionAge.String(),
		MaxConnectionAgeGrace: helper.RPCMaxConnectionAgeGrace.String(),
	}, be)
	if err != nil {
		log.Fatal(err)
	}

	if err := testRPCServer.Start(); err != nil {
		log.Fatalf("failed rpc listen: %s\n", err)
	}

	var dialOptions []grpc.DialOption
	authInterceptor := client.NewAuthInterceptor(project.PublicKey, "")
	dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(authInterceptor.Unary()))
	dialOptions = append(dialOptions, grpc.WithStreamInterceptor(authInterceptor.Stream()))
	dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))

	conn, err := grpc.Dial(testRPCAddr, dialOptions...)
	if err != nil {
		log.Fatal(err)
	}
	testClient = api.NewYorkieServiceClient(conn)

	credentials := grpc.WithTransportCredentials(insecure.NewCredentials())
	dialOptions = []grpc.DialOption{credentials}

	testAdminAuthInterceptor = admin.NewAuthInterceptor("")
	dialOptions = append(dialOptions, grpc.WithUnaryInterceptor(testAdminAuthInterceptor.Unary()))
	dialOptions = append(dialOptions, grpc.WithStreamInterceptor(testAdminAuthInterceptor.Stream()))

	adminConn, err := grpc.Dial(testRPCAddr, dialOptions...)
	if err != nil {
		log.Fatal(err)
	}
	testAdminClient = api.NewAdminServiceClient(adminConn)

	code := m.Run()

	if err := be.Shutdown(); err != nil {
		log.Fatal(err)
	}
	testRPCServer.Shutdown(true)
	os.Exit(code)
}

func TestSDKRPCServerBackendWithShardedDB(t *testing.T) {
	t.Run("activate/deactivate client test", func(t *testing.T) {
		testcases.RunActivateAndDeactivateClientTest(t, testClient)
	})

	t.Run("attach/detach document test", func(t *testing.T) {
		testcases.RunAttachAndDetachDocumentTest(t, testClient)
	})

	t.Run("attach/detach on removed document test", func(t *testing.T) {
		testcases.RunAttachAndDetachRemovedDocumentTest(t, testClient)
	})

	t.Run("push/pull changes test", func(t *testing.T) {
		testcases.RunPushPullChangeTest(t, testClient)
	})

	t.Run("push/pull on removed document test", func(t *testing.T) {
		testcases.RunPushPullChangeOnRemovedDocumentTest(t, testClient)
	})

	t.Run("remove document test", func(t *testing.T) {
		testcases.RunRemoveDocumentTest(t, testClient)
	})

	t.Run("remove document with invalid client state test", func(t *testing.T) {
		testcases.RunRemoveDocumentWithInvalidClientStateTest(t, testClient)
	})

	t.Run("watch document test", func(t *testing.T) {
		testcases.RunWatchDocumentTest(t, testClient)
	})
}

func TestAdminRPCServerBackendWithShardedDB(t *testing.T) {
	t.Run("admin signup test", func(t *testing.T) {
		testcases.RunAdminSignUpTest(t, testAdminClient)
	})

	t.Run("admin login test", func(t *testing.T) {
		testcases.RunAdminLoginTest(t, testAdminClient)
	})

	t.Run("admin create project test", func(t *testing.T) {
		testcases.RunAdminCreateProjectTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin list projects test", func(t *testing.T) {
		testcases.RunAdminListProjectsTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin get project test", func(t *testing.T) {
		testcases.RunAdminGetProjectTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin update project test", func(t *testing.T) {
		testcases.RunAdminUpdateProjectTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin list documents test", func(t *testing.T) {
		testcases.RunAdminListDocumentsTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin get document test", func(t *testing.T) {
		testcases.RunAdminGetDocumentTest(t, testClient, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin list changes test", func(t *testing.T) {
		testcases.RunAdminListChangesTest(t, testClient, testAdminClient, testAdminAuthInterceptor)
	})
}
