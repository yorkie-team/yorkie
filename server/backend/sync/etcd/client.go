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

// Package etcd provides etcd implementation of the sync. It is used to
// synchronize the state of the cluster.
package etcd

import (
	"context"
	"fmt"
	gosync "sync"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/backend/sync/memory"
	"github.com/yorkie-team/yorkie/server/logging"
)

// clusterClientInfo represents a cluster client and its connection.
type clusterClientInfo struct {
	client api.ClusterServiceClient
	conn   *grpc.ClientConn
}

// Client is a client that connects to ETCD.
type Client struct {
	config     *Config
	serverInfo *sync.ServerInfo

	localPubSub *memory.PubSub

	memberMapMu        *gosync.RWMutex
	memberMap          map[string]*sync.ServerInfo
	clusterClientMapMu *gosync.RWMutex
	clusterClientMap   map[string]*clusterClientInfo

	client *clientv3.Client

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// newClient creates a new instance of Client.
func newClient(
	conf *Config,
	serverInfo *sync.ServerInfo,
) *Client {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Client{
		config:     conf,
		serverInfo: serverInfo,

		localPubSub: memory.NewPubSub(),

		memberMapMu:        &gosync.RWMutex{},
		memberMap:          make(map[string]*sync.ServerInfo),
		clusterClientMapMu: &gosync.RWMutex{},
		clusterClientMap:   make(map[string]*clusterClientInfo),

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}
}

// Dial creates a new instance of Client and dials the given ETCD.
func Dial(
	conf *Config,
	serverInfo *sync.ServerInfo,
) (
	*Client, error) {
	c := newClient(conf, serverInfo)

	if err := c.Dial(); err != nil {
		return nil, err
	}

	return c, nil
}

// Dial dials the given ETCD.
func (c *Client) Dial() error {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   c.config.Endpoints,
		DialTimeout: c.config.ParseDialTimeout(),
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
		Username:    c.config.Username,
		Password:    c.config.Password,
	})
	if err != nil {
		return fmt.Errorf("connect to etcd: %w", err)
	}

	logging.DefaultLogger().Infof("etcd connected, URI: %s", c.config.Endpoints)

	c.client = cli
	return nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	c.cancelFunc()

	if err := c.removeServerInfo(context.Background()); err != nil {
		logging.DefaultLogger().Error(err)
	}

	for id := range c.clusterClientMap {
		c.removeClusterClient(id)
	}

	if err := c.client.Close(); err != nil {
		return fmt.Errorf("close etcd client: %w", err)
	}

	return nil
}
