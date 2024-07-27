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
	"net/http"
	"os"
	"testing"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/server/rpc/testcases"
	"github.com/yorkie-team/yorkie/test/helper"
)

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
		AdminUser:                 helper.AdminUser,
		AdminPassword:             helper.AdminPassword,
		UseDefaultProject:         helper.UseDefaultProject,
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

	t.Run("admin delete account test", func(t *testing.T) {
		testcases.RunAdminDeleteAccountTest(t, testAdminClient)
	})

	t.Run("admin change password test", func(t *testing.T) {
		testcases.RunAdminChangePasswordTest(t, testAdminClient)
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
