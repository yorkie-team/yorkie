package rpc_test

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

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"testing"

	"golang.org/x/net/context"
	"golang.org/x/net/nettest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yorkie-team/yorkie/admin"
	yorkieTypes "github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	defaultProjectID = yorkieTypes.ID("000000000000000000000000")

	defaultProjectName = "default"
	invalidSlugName    = "@#$%^&*()_+"

	nilClientID, _     = hex.DecodeString("000000000000000000000000")
	emptyClientID, _   = hex.DecodeString("")
	invalidClientID, _ = hex.DecodeString("invalid")

	testRPCServer            *rpc.Server
	testRPCAddr              = fmt.Sprintf("localhost:%d", helper.RPCPort)
	testClient               api.YorkieServiceClient
	testAdminAuthInterceptor *admin.AuthInterceptor
	testAdminClient          api.AdminServiceClient

	invalidChangePack = &api.ChangePack{
		DocumentKey: "invalid",
		Checkpoint:  nil,
	}

	be *backend.Backend
)

type testAdminServer struct {
	grpcServer  *grpc.Server
	adminServer *api.UnimplementedAdminServiceServer
}

// dialTestYorkieServer creates a new instance of testYorkieServer and
// dials it with LocalListener.
func dialTestAdminServer(t *testing.T) (*testAdminServer, string) {
	adminServer := &api.UnimplementedAdminServiceServer{}
	grpcServer := grpc.NewServer()
	api.RegisterAdminServiceServer(grpcServer, adminServer)

	testAdminServer := &testAdminServer{
		grpcServer:  grpcServer,
		adminServer: adminServer,
	}

	return testAdminServer, testAdminServer.listenAndServe(t)
}

func (s *testAdminServer) listenAndServe(t *testing.T) string {
	lis, err := nettest.NewLocalListener("tcp")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			if err != grpc.ErrServerStopped {
				t.Error(err)
			}
		}
	}()

	return lis.Addr().String()
}

func (s *testAdminServer) Stop() {
	s.grpcServer.Stop()
}

func TestMain(m *testing.M) {
	met, err := prometheus.NewMetrics()
	if err != nil {
		log.Fatal(err)
	}

	be, err = backend.New(&backend.Config{
		AdminUser:                 helper.AdminUser,
		AdminPassword:             helper.AdminPassword,
		ClientDeactivateThreshold: helper.ClientDeactivateThreshold,
		SnapshotThreshold:         helper.SnapshotThreshold,
		AuthWebhookCacheSize:      helper.AuthWebhookSize,
		AdminTokenDuration:        helper.AdminTokenDuration,
	}, &mongo.Config{
		ConnectionURI:     helper.MongoConnectionURI,
		YorkieDatabase:    helper.TestDBName(),
		ConnectionTimeout: helper.MongoConnectionTimeout,
		PingTimeout:       helper.MongoPingTimeout,
	}, &housekeeping.Config{
		Interval:                  helper.HousekeepingInterval.String(),
		CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
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
