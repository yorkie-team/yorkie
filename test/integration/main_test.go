// +build integration

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

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie"
)

type clientAndDocPair struct {
	cli *client.Client
	doc *document.Document
}

type watchResponsePair struct {
	Type  client.WatchResponseType
	peers map[string]client.Metadata
}

var defaultAgent *yorkie.Yorkie

func TestMain(m *testing.M) {
	agent := helper.TestYorkie()
	if err := agent.Start(); err != nil {
		log.Logger.Fatal(err)
	}
	defaultAgent = agent
	code := m.Run()
	if defaultAgent != nil {
		if err := defaultAgent.Shutdown(true); err != nil {
			log.Logger.Error(err)
		}
	}
	os.Exit(code)
}

func syncClientsThenAssertEqual(t *testing.T, pairs []clientAndDocPair) {
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
		assert.Equal(t, expected, v)
	}
}

func createConn() (*grpc.ClientConn, error) {
	conn, err := grpc.Dial(defaultAgent.RPCAddr(), grpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func createActivatedClients(t *testing.T, n int) (clients []*client.Client) {
	for i := 0; i < n; i++ {
		c, err := client.Dial(
			defaultAgent.RPCAddr(),
			client.Option{Metadata: map[string]string{
				"name": fmt.Sprintf("name-%d", i),
			}},
		)
		assert.NoError(t, err)

		err = c.Activate(context.Background())
		assert.NoError(t, err)

		clients = append(clients, c)
	}

	return
}

func cleanupClients(t *testing.T, clients []*client.Client) {
	for _, c := range clients {
		err := c.Deactivate(context.Background())
		assert.NoError(t, err)

		err = c.Close()
		assert.NoError(t, err)
	}
}
