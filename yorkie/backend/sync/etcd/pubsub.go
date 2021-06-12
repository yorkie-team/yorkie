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

	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// Subscribe subscribes to the given topics.
func (c *Client) Subscribe(
	subscriber types.Client,
	topics []*key.Key,
) (*sync.Subscription, map[string][]types.Client, error) {
	// TODO(hackerwins): build peersMap.
	return c.pubSub.Subscribe(subscriber, topics)
}

// Unsubscribe unsubscribes the given topics.
func (c *Client) Unsubscribe(topics []*key.Key, sub *sync.Subscription) {
	c.pubSub.Unsubscribe(topics, sub)
}

// Publish publishes the given event to the given Topic.
func (c *Client) Publish(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	c.PublishToLocal(ctx, publisherID, event)

	for _, member := range c.Members() {
		memberAddr := member.RPCAddr
		if memberAddr == c.agentInfo.RPCAddr {
			continue
		}

		clientInfo, err := c.ensureClusterClient(member)
		if err != nil {
			continue
		}

		if err := c.publishToMember(
			ctx,
			clientInfo,
			publisherID,
			event,
		); err != nil {
			continue
		}
	}
}

// PublishToLocal publishes the given event to the given Topic.
func (c *Client) PublishToLocal(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	c.pubSub.Publish(ctx, publisherID, event)
}

// ensureClusterClient return the cluster client from the cache or creates it.
func (c *Client) ensureClusterClient(member *sync.AgentInfo) (*clusterClientInfo, error) {
	c.clusterClientMapMu.Lock()
	defer c.clusterClientMapMu.Unlock()

	if _, ok := c.clusterClientMap[member.ID]; !ok {
		conn, err := grpc.Dial(member.RPCAddr, grpc.WithInsecure())
		if err != nil {
			log.Logger.Error(err)
			return nil, err
		}

		c.clusterClientMap[member.ID] = &clusterClientInfo{
			client: api.NewClusterClient(conn),
			conn:   conn,
		}
	}

	return c.clusterClientMap[member.ID], nil
}

// removeClusterClient removes the cluster client of the given ID.
func (c *Client) removeClusterClient(id string) {
	c.clusterClientMapMu.Lock()
	defer c.clusterClientMapMu.Unlock()

	if info, ok := c.clusterClientMap[id]; ok {
		if err := info.conn.Close(); err != nil {
			log.Logger.Error(err)
		}

		delete(c.clusterClientMap, id)
	}
}

// publishToMember publishes events to other agents.
func (c *Client) publishToMember(
	ctx context.Context,
	clientInfo *clusterClientInfo,
	publisherID *time.ActorID,
	event sync.DocEvent,
) error {
	docEvent, err := converter.ToDocEvent(event)
	if err != nil {
		return err
	}

	if _, err := clientInfo.client.BroadcastEvent(ctx, &api.BroadcastEventRequest{
		PublisherId: publisherID.Bytes(),
		Event:       docEvent,
	}); err != nil {
		return errors.WithStack(err)
	}

	return nil
}
