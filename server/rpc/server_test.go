/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package rpc_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/sync/etcd"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/server/rpc"
	"github.com/yorkie-team/yorkie/test/helper"
)

var (
	nilClientID, _     = hex.DecodeString("000000000000000000000000")
	emptyClientID, _   = hex.DecodeString("")
	invalidClientID, _ = hex.DecodeString("invalid")

	testRPCServer *rpc.Server
	testRPCAddr   = fmt.Sprintf("localhost:%d", helper.RPCPort)
	testAdminAddr = fmt.Sprintf("localhost:%d", helper.AdminPort)
	testClient    api.YorkieServiceClient

	invalidChangePack = &api.ChangePack{
		DocumentKey: "invalid",
		Checkpoint:  nil,
	}
)

func TestMain(m *testing.M) {
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
	}, &mongo.Config{
		ConnectionURI:     helper.MongoConnectionURI,
		YorkieDatabase:    helper.TestDBName(),
		ConnectionTimeout: helper.MongoConnectionTimeout,
		PingTimeout:       helper.MongoPingTimeout,
	}, &etcd.Config{
		Endpoints:     helper.ETCDEndpoints,
		DialTimeout:   helper.ETCDDialTimeout.String(),
		LockLeaseTime: helper.ETCDLockLeaseTime.String(),
	}, &housekeeping.Config{
		Interval:                  helper.HousekeepingInterval.String(),
		CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
	}, testAdminAddr, met)
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
		Port:            helper.RPCPort,
		MaxRequestBytes: helper.RPCMaxRequestBytes,
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

	code := m.Run()

	if err := be.Shutdown(); err != nil {
		log.Fatal(err)
	}
	testRPCServer.Shutdown(true)
	os.Exit(code)
}

func TestRPCServerBackend(t *testing.T) {
	t.Run("activate/deactivate client test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		_, err = testClient.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
		)
		assert.NoError(t, err)

		// invalid argument
		_, err = testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: ""},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = testClient.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: emptyClientID},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		// client not found
		_, err = testClient.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: nilClientID},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())
	})

	t.Run("attach/detach document test", func(t *testing.T) {
		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: t.Name()},
		)
		assert.NoError(t, err)

		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to attach with invalid client ID
		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   invalidClientID,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		// try to attach with invalid client
		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   nilClientID,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())

		// try to attach already attached document
		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

		// try to attach invalid change pack
		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: invalidChangePack,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = testClient.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.NoError(t, err)

		// try to detach already detached document
		_, err = testClient.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

		_, err = testClient.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: invalidChangePack,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		// document not found
		_, err = testClient.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId: activateResp.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: "invalid",
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
				},
			},
		)
		assert.Equal(t, codes.Unknown, status.Convert(err).Code())

		_, err = testClient.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
		)
		assert.NoError(t, err)

		// try to attach the document with a deactivated client
		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
	})

	t.Run("push/pull changes test", func(t *testing.T) {
		packWithNoChanges := &api.ChangePack{
			DocumentKey: helper.TestDocKey(t),
			Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
		}

		activateResp, err := testClient.ActivateClient(
			context.Background(),
			&api.ActivateClientRequest{ClientKey: helper.TestDocKey(t)},
		)
		assert.NoError(t, err)

		_, err = testClient.AttachDocument(
			context.Background(),
			&api.AttachDocumentRequest{
				ClientId: activateResp.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 1},
					Changes: []*api.Change{{
						Id: &api.ChangeID{
							ClientSeq: 1,
							Lamport:   1,
							ActorId:   activateResp.ClientId,
						},
					}},
				},
			},
		)
		assert.NoError(t, err)

		_, err = testClient.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId: activateResp.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 2},
					Changes: []*api.Change{{
						Id: &api.ChangeID{
							ClientSeq: 2,
							Lamport:   2,
							ActorId:   activateResp.ClientId,
						},
					}},
				},
			},
		)
		assert.NoError(t, err)

		_, err = testClient.DetachDocument(
			context.Background(),
			&api.DetachDocumentRequest{
				ClientId: activateResp.ClientId,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestDocKey(t),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 3},
					Changes: []*api.Change{{
						Id: &api.ChangeID{
							ClientSeq: 3,
							Lamport:   3,
							ActorId:   activateResp.ClientId,
						},
					}},
				},
			},
		)
		assert.NoError(t, err)

		// try to push/pull with detached document
		_, err = testClient.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())

		// try to push/pull with invalid pack
		_, err = testClient.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: invalidChangePack,
			},
		)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = testClient.DeactivateClient(
			context.Background(),
			&api.DeactivateClientRequest{ClientId: activateResp.ClientId},
		)
		assert.NoError(t, err)

		// try to push/pull with deactivated client
		_, err = testClient.PushPull(
			context.Background(),
			&api.PushPullRequest{
				ClientId:   activateResp.ClientId,
				ChangePack: packWithNoChanges,
			},
		)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
	})
}

func TestConfig_Validate(t *testing.T) {
	scenarios := []*struct {
		config   *rpc.Config
		expected error
	}{
		{config: &rpc.Config{Port: -1}, expected: rpc.ErrInvalidRPCPort},
		{config: &rpc.Config{Port: 11101, CertFile: "noSuchCertFile"}, expected: rpc.ErrInvalidCertFile},
		{config: &rpc.Config{Port: 11101, KeyFile: "noSuchKeyFile"}, expected: rpc.ErrInvalidKeyFile},
		// not to use tls
		{config: &rpc.Config{Port: 11101, CertFile: "", KeyFile: ""}, expected: nil},
		// pass any file existing
		{config: &rpc.Config{Port: 11101, CertFile: "server_test.go", KeyFile: "server_test.go"}, expected: nil},
	}
	for _, scenario := range scenarios {
		assert.ErrorIs(t, scenario.config.Validate(), scenario.expected, "provided config: %#v", scenario.config)
	}
}
