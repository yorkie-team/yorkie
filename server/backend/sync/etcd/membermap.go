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
	"encoding/json"
	"fmt"
	"path"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	serversPath         = "/servers"
	putServerInfoPeriod = 5 * time.Second
	serverValueTTL      = 7 * time.Second
)

// Initialize put this server to etcd with TTL periodically.
func (c *Client) Initialize() error {
	ctx := context.Background()
	if err := c.putServerInfo(ctx); err != nil {
		return err
	}
	if err := c.initializeMemberMap(ctx); err != nil {
		return err
	}

	go c.syncServerInfos()
	go c.putServerPeriodically()

	return nil
}

// Members returns the members of this cluster.
func (c *Client) Members() map[string]*sync.ServerInfo {
	c.memberMapMu.RLock()
	defer c.memberMapMu.RUnlock()

	memberMap := make(map[string]*sync.ServerInfo)
	for _, member := range c.memberMap {
		memberMap[member.ID] = &sync.ServerInfo{
			ID:          member.ID,
			Hostname:    member.Hostname,
			ClusterAddr: member.ClusterAddr,
			UpdatedAt:   member.UpdatedAt,
		}
	}

	return memberMap
}

// initializeMemberMap initializes the local member map by loading data from etcd.
func (c *Client) initializeMemberMap(ctx context.Context) error {
	getResponse, err := c.client.Get(ctx, serversPath, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("get %s: %w", serversPath, err)
	}

	for _, kv := range getResponse.Kvs {
		var info sync.ServerInfo
		if err := json.Unmarshal(kv.Value, &info); err != nil {
			return fmt.Errorf("unmarshal %s: %w", kv.Key, err)
		}

		c.setServerInfo(string(kv.Key), info)
	}
	return nil
}

// putServerPeriodically puts the local server in etcd periodically.
func (c *Client) putServerPeriodically() {
	for {
		if err := c.putServerInfo(c.ctx); err != nil {
			logging.DefaultLogger().Error(err)
		}

		select {
		case <-time.After(putServerInfoPeriod):
		case <-c.ctx.Done():
			return
		}
	}
}

// putServerInfo puts the local server in etcd.
func (c *Client) putServerInfo(ctx context.Context) error {
	grantResponse, err := c.client.Grant(ctx, int64(serverValueTTL.Seconds()))
	if err != nil {
		return fmt.Errorf("grant %s: %w", c.serverInfo.ID, err)
	}

	serverInfo := *c.serverInfo
	serverInfo.UpdatedAt = time.Now()
	bytes, err := json.Marshal(serverInfo)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", c.serverInfo.ID, err)
	}

	k := path.Join(serversPath, c.serverInfo.ID)
	_, err = c.client.Put(ctx, k, string(bytes), clientv3.WithLease(grantResponse.ID))
	if err != nil {
		return fmt.Errorf("put %s: %w", k, err)
	}
	return nil
}

// removeServerInfo removes the local server in etcd.
func (c *Client) removeServerInfo(ctx context.Context) error {
	k := path.Join(serversPath, c.serverInfo.ID)
	_, err := c.client.Delete(ctx, k)
	if err != nil {
		return fmt.Errorf("remove %s: %w", k, err)
	}
	return nil
}

// syncServerInfos syncs the local member map with etcd.
func (c *Client) syncServerInfos() {
	// TODO(hackerwins): When the network is recovered, check if we need to
	// recover the channels watched in the situation.
	watchCh := c.client.Watch(c.ctx, serversPath, clientv3.WithPrefix())
	for {
		select {
		case watchResponse := <-watchCh:
			for _, event := range watchResponse.Events {
				k := string(event.Kv.Key)
				switch event.Type {
				case mvccpb.PUT:
					var info sync.ServerInfo
					if err := json.Unmarshal(event.Kv.Value, &info); err != nil {
						logging.DefaultLogger().Error(err)
						continue
					}
					c.setServerInfo(k, info)
				case mvccpb.DELETE:
					c.deleteServerInfo(k)
					c.removeClusterClient(k)
				}
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// setServerInfo sets the given serverInfo to the local member map.
func (c *Client) setServerInfo(key string, value sync.ServerInfo) {
	c.memberMapMu.Lock()
	defer c.memberMapMu.Unlock()

	c.memberMap[key] = &value
}

// deleteServerInfo removes the given serverInfo from the local member map.
func (c *Client) deleteServerInfo(id string) {
	c.memberMapMu.Lock()
	defer c.memberMapMu.Unlock()

	delete(c.memberMap, id)
}
