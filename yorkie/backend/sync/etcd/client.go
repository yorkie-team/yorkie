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
	"time"

	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/clientv3/concurrency"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/memory"
)

const (
	// DefaultDialTimeoutSec is the default dial timeout of etcd connection.
	DefaultDialTimeoutSec = 5

	// DefaultLockLeaseTimeSec is the default lease time of lock.
	DefaultLockLeaseTimeSec = 30
)

// Config is the configuration for creating a Client instance.
type Config struct {
	Endpoints      []string      `json:"Endpoints"`
	DialTimeoutSec time.Duration `json:"DialTimeoutSec"`
	Username       string        `json:"Username"`
	Password       string        `json:"Password"`

	LockLeaseTimeSec int `json:"LockLeaseTimeSec"`
}

// broadcastClientInfo manages broadcast grpc server connections and clients.
type broadcastClientInfo struct {
	client api.BroadcastClient
	conn   *grpc.ClientConn
}

// Client is a client that connects to ETCD.
type Client struct {
	config *Config
	client *clientv3.Client

	pubSub *memory.PubSub

	memberMapMu        *gosync.RWMutex
	memberMap          map[string]*sync.AgentInfo
	broadcastClientMap map[string]*broadcastClientInfo

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// newClient creates a new instance of Client.
func newClient(conf *Config, agentInfo *sync.AgentInfo) *Client {
	if conf.DialTimeoutSec == 0 {
		conf.DialTimeoutSec = DefaultDialTimeoutSec
	}
	if conf.LockLeaseTimeSec == 0 {
		conf.LockLeaseTimeSec = DefaultLockLeaseTimeSec
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Client{
		config: conf,

		pubSub: memory.NewPubSub(agentInfo),

		memberMapMu:        &gosync.RWMutex{},
		memberMap:          make(map[string]*sync.AgentInfo),
		broadcastClientMap: make(map[string]*broadcastClientInfo),

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}
}

// Dial creates a new instance of Client and dials the given ETCD.
func Dial(conf *Config, agentInfo *sync.AgentInfo) (*Client, error) {
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
		DialTimeout: c.config.DialTimeoutSec * time.Second,
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
		Username:    c.config.Username,
		Password:    c.config.Password,
	})
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	log.Logger.Infof("etcd connected, URI: %s", c.config.Endpoints)

	c.client = cli
	return nil
}

// Close all resources of this client.
func (c *Client) Close() error {
	c.cancelFunc()

	if err := c.removeAgent(context.Background()); err != nil {
		log.Logger.Error(err)
	}

	return c.client.Close()
}

// NewLocker creates locker of the given key.
func (c *Client) NewLocker(
	ctx context.Context,
	key sync.Key,
) (sync.Locker, error) {
	session, err := concurrency.NewSession(
		c.client,
		concurrency.WithContext(ctx),
		concurrency.WithTTL(c.config.LockLeaseTimeSec),
	)
	if err != nil {
		log.Logger.Error(err)
		return nil, err
	}

	return &internalLocker{
		session,
		concurrency.NewMutex(session, key.String()),
	}, nil
}
