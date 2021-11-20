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

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

const (
	agentsPath     = "/agents"
	putAgentPeriod = 5 * time.Second
	agentValueTTL  = 7 * time.Second
)

// Initialize put this agent to etcd with TTL periodically.
func (c *Client) Initialize() error {
	ctx := context.Background()
	if err := c.putAgent(ctx); err != nil {
		return err
	}
	if err := c.initializeMemberMap(ctx); err != nil {
		return err
	}

	go c.syncAgents()
	go c.putAgentPeriodically()

	return nil
}

// Members returns the members of this cluster.
func (c *Client) Members() map[string]*sync.AgentInfo {
	c.memberMapMu.RLock()
	defer c.memberMapMu.RUnlock()

	memberMap := make(map[string]*sync.AgentInfo)
	for _, member := range c.memberMap {
		memberMap[member.ID] = &sync.AgentInfo{
			ID:        member.ID,
			Hostname:  member.Hostname,
			RPCAddr:   member.RPCAddr,
			UpdatedAt: member.UpdatedAt,
		}
	}

	return memberMap
}

// initializeMemberMap initializes the local member map by loading data from etcd.
func (c *Client) initializeMemberMap(ctx context.Context) error {
	getResponse, err := c.client.Get(ctx, agentsPath, clientv3.WithPrefix())
	if err != nil {
		return err
	}

	for _, kv := range getResponse.Kvs {
		var info sync.AgentInfo
		if err := json.Unmarshal(kv.Value, &info); err != nil {
			return err
		}

		c.setAgentInfo(string(kv.Key), info)
	}
	return nil
}

// putAgentPeriodically puts the local agent in etcd periodically.
func (c *Client) putAgentPeriodically() {
	for {
		if err := c.putAgent(c.ctx); err != nil {
			log.Logger.Error(err)
		}

		select {
		case <-time.After(putAgentPeriod):
		case <-c.ctx.Done():
			return
		}
	}
}

// putAgent puts the local agent in etcd.
func (c *Client) putAgent(ctx context.Context) error {
	grantResponse, err := c.client.Grant(ctx, int64(agentValueTTL.Seconds()))
	if err != nil {
		return fmt.Errorf("grant %s: %w", c.agentInfo.ID, err)
	}

	agentInfo := *c.agentInfo
	agentInfo.UpdatedAt = time.Now()
	bytes, err := json.Marshal(agentInfo)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", c.agentInfo.ID, err)
	}

	k := path.Join(agentsPath, c.agentInfo.ID)
	_, err = c.client.Put(ctx, k, string(bytes), clientv3.WithLease(grantResponse.ID))
	if err != nil {
		return fmt.Errorf("put %s: %w", k, err)
	}
	return nil
}

// removeAgent removes the local agent in etcd.
func (c *Client) removeAgent(ctx context.Context) error {
	k := path.Join(agentsPath, c.agentInfo.ID)
	_, err := c.client.Delete(ctx, k)
	if err != nil {
		return fmt.Errorf("remove %s: %w", k, err)
	}
	return nil
}

// syncAgents syncs the local member map with etcd.
func (c *Client) syncAgents() {
	// TODO(hackerwins): When the network is recovered, check if we need to
	// recover the channels watched in the situation.
	watchCh := c.client.Watch(c.ctx, agentsPath, clientv3.WithPrefix())
	for {
		select {
		case watchResponse := <-watchCh:
			for _, event := range watchResponse.Events {
				k := string(event.Kv.Key)
				switch event.Type {
				case mvccpb.PUT:
					var info sync.AgentInfo
					if err := json.Unmarshal(event.Kv.Value, &info); err != nil {
						log.Logger.Error(err)
						continue
					}
					c.setAgentInfo(k, info)
				case mvccpb.DELETE:
					c.removeAgentInfo(k)
					c.removeClusterClient(k)
				}
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// setAgentInfo sets the given agentInfo to the local member map.
func (c *Client) setAgentInfo(key string, value sync.AgentInfo) {
	c.memberMapMu.Lock()
	defer c.memberMapMu.Unlock()

	c.memberMap[key] = &value
}

// removeAgentInfo removes the given agentInfo from the local member map.
func (c *Client) removeAgentInfo(id string) {
	c.memberMapMu.Lock()
	defer c.memberMapMu.Unlock()

	delete(c.memberMap, id)
}
