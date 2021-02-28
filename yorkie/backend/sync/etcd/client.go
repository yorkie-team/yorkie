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
	"time"

	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/clientv3/concurrency"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
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

// Client is a client that connects to ETCD.
type Client struct {
	config *Config
	client *clientv3.Client
}

// newClient creates a new instance of Client.
func newClient(conf *Config) *Client {
	if conf.DialTimeoutSec == 0 {
		conf.DialTimeoutSec = DefaultDialTimeoutSec
	}
	if conf.LockLeaseTimeSec == 0 {
		conf.LockLeaseTimeSec = DefaultLockLeaseTimeSec
	}

	return &Client{
		config: conf,
	}
}

// Dial creates a new instance of Client and dials the given ETCD.
func Dial(conf *Config) (*Client, error) {
	c := newClient(conf)

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
