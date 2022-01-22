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

package etcd

import (
	"context"
	gosync "sync"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
	"github.com/yorkie-team/yorkie/yorkie/log"
)

// clusterClientInfo represents a cluster client and its connection.
type clusterClientInfo struct {
	client api.ClusterClient
	conn   *grpc.ClientConn
}

// Client is a client that connects to ETCD.
type Client struct {
	config    *Config
	agentInfo *sync.AgentInfo

	localPubSub *memory.PubSub

	memberMapMu        *gosync.RWMutex
	memberMap          map[string]*sync.AgentInfo
	clusterClientMapMu *gosync.RWMutex
	clusterClientMap   map[string]*clusterClientInfo

	client *clientv3.Client

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// newClient creates a new instance of Client.
func newClient(
	conf *Config,
	agentInfo *sync.AgentInfo,
) *Client {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Client{
		config:    conf,
		agentInfo: agentInfo,

		localPubSub: memory.NewPubSub(),

		memberMapMu:        &gosync.RWMutex{},
		memberMap:          make(map[string]*sync.AgentInfo),
		clusterClientMapMu: &gosync.RWMutex{},
		clusterClientMap:   make(map[string]*clusterClientInfo),

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}
}

// Dial creates a new instance of Client and dials the given ETCD.
func Dial(
	conf *Config,
	agentInfo *sync.AgentInfo,
) (
	*Client, error) {
	c := newClient(conf, agentInfo)

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
		log.Logger().Error(err)
		return err
	}

	log.Logger().Infof("etcd connected, URI: %s", c.config.Endpoints)

	c.client = cli
	return nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	c.cancelFunc()

	if err := c.removeAgent(context.Background()); err != nil {
		log.Logger().Error(err)
	}

	for id := range c.clusterClientMap {
		c.removeClusterClient(id)
	}

	return c.client.Close()
}
