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

	"google.golang.org/grpc"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// Subscribe subscribes to the given topics.
func (c *Client) Subscribe(
	subscriber types.Client,
	topics []string,
) (*sync.Subscription, map[string][]types.Client, error) {
	// TODO(hackerwins): build peersMap.
	return c.pubSub.Subscribe(subscriber, topics)
}

// Unsubscribe unsubscribes the given topics.
func (c *Client) Unsubscribe(topics []string, sub *sync.Subscription) {
	c.pubSub.Unsubscribe(topics, sub)
}

// Publish publishes the given event to the given Topic.
func (c *Client) Publish(
	publisherID *time.ActorID,
	topic string,
	event sync.DocEvent,
) {
	c.PublishToLocal(publisherID, topic, event)

	for _, member := range c.Members() {
		memberAddr := member.RPCAddr
		if memberAddr == c.pubSub.AgentInfo.RPCAddr {
			continue
		}

		clientInfo, err := c.ensureClusterClient(memberAddr)
		if err != nil {
			continue
		}

		if err := c.publishToMember(
			context.Background(),
			clientInfo,
			publisherID,
			topic,
			event,
		); err != nil {
			continue
		}
	}
}

// PublishToLocal publishes the given event to the given Topic.
func (c *Client) PublishToLocal(
	publisherID *time.ActorID,
	topic string,
	event sync.DocEvent,
) {
	c.pubSub.Publish(publisherID, topic, event)
}

// ensureClusterClient activates the cluster grpc server and creates a client.
func (c *Client) ensureClusterClient(addr string) (*clusterClientInfo, error) {
	c.clusterClientMapMu.Lock()
	defer c.clusterClientMapMu.Unlock()

	if _, ok := c.clusterClientMap[addr]; !ok {
		conn, err := grpc.Dial(addr, grpc.WithInsecure())
		if err != nil {
			log.Logger.Error(err)
			return nil, err
		}

		c.clusterClientMap[addr] = &clusterClientInfo{
			client: api.NewClusterClient(conn),
			conn:   conn,
		}
	}

	return c.clusterClientMap[addr], nil
}

// publishToMember publishes events to other agents.
func (c *Client) publishToMember(
	ctx context.Context,
	clientInfo *clusterClientInfo,
	publisherID *time.ActorID,
	topic string,
	event sync.DocEvent,
) error {
	docEvent, err := converter.ToDocEvent(event)
	if err != nil {
		log.Logger.Error(err)
		return err
	}

	if _, err := clientInfo.client.BroadcastEvent(ctx, &api.BroadcastEventRequest{
		PublisherId: publisherID.Bytes(),
		Topic:       topic,
		DocEvent:    docEvent,
	}); err != nil {
		log.Logger.Error(err)
		return err
	}

	return nil
}
