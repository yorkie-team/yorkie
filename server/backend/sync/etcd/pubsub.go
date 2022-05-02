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
	"fmt"
	"path"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yorkie-team/yorkie/api"
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	subscriptionsPath = "/subscriptions"
)

// Subscribe subscribes to the given keys.
func (c *Client) Subscribe(
	ctx context.Context,
	subscriber types.Client,
	keys []key.Key,
) (*sync.Subscription, map[string][]types.Client, error) {
	sub, err := c.localPubSub.Subscribe(ctx, subscriber, keys)
	if err != nil {
		return nil, nil, err
	}

	// TODO(hackerwins): If the server is not stopped gracefully, there may
	// be garbage subscriptions left. Consider introducing a TTL and
	// updating it periodically.
	if err := c.putSubscriptions(ctx, keys, sub); err != nil {
		return nil, nil, err
	}

	peersMap := make(map[string][]types.Client)
	for _, k := range keys {
		subs, err := c.pullSubscriptions(ctx, k)
		if err != nil {
			return nil, nil, err
		}

		peersMap[k.String()] = subs
	}

	return sub, peersMap, nil
}

// Unsubscribe unsubscribes the given keys.
func (c *Client) Unsubscribe(
	ctx context.Context,
	keys []key.Key,
	sub *sync.Subscription,
) error {
	c.localPubSub.Unsubscribe(ctx, keys, sub)
	return c.removeSubscriptions(ctx, keys, sub)
}

// Publish publishes the given event.
func (c *Client) Publish(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	c.localPubSub.Publish(ctx, publisherID, event)
	c.broadcastToMembers(ctx, event)
}

// PublishToLocal publishes the given event.
func (c *Client) PublishToLocal(
	ctx context.Context,
	publisherID *time.ActorID,
	event sync.DocEvent,
) {
	c.localPubSub.Publish(ctx, publisherID, event)
}

// UpdateMetadata updates the metadata of the given client.
func (c *Client) UpdateMetadata(
	ctx context.Context,
	publisher *types.Client,
	keys []key.Key,
) (*sync.DocEvent, error) {
	if sub := c.localPubSub.UpdateMetadata(publisher, keys); sub != nil {
		if err := c.putSubscriptions(ctx, keys, sub); err != nil {
			return nil, err
		}
	}

	return &sync.DocEvent{
		Type:         types.MetadataChangedEvent,
		Publisher:    *publisher,
		DocumentKeys: keys,
	}, nil
}

// broadcastToMembers broadcasts the given event to all members.
func (c *Client) broadcastToMembers(ctx context.Context, event sync.DocEvent) {
	for _, member := range c.Members() {
		memberAddr := member.ClusterAddr
		if memberAddr == c.serverInfo.ClusterAddr {
			continue
		}

		clientInfo, err := c.ensureClusterClient(member)
		if err != nil {
			continue
		}

		if err := c.publishToMember(
			ctx,
			clientInfo,
			event.Publisher.ID,
			event,
		); err != nil {
			continue
		}
	}
}

// ensureClusterClient return the cluster client from the cache or creates it.
func (c *Client) ensureClusterClient(
	member *sync.ServerInfo,
) (*clusterClientInfo, error) {
	c.clusterClientMapMu.Lock()
	defer c.clusterClientMapMu.Unlock()

	// TODO(hackerwins): If the connection is disconnected, the client should
	// be removed from clusterClientMap.

	if _, ok := c.clusterClientMap[member.ID]; !ok {
		conn, err := grpc.Dial(member.ClusterAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			logging.DefaultLogger().Error(err)
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
			logging.DefaultLogger().Error(err)
		}

		delete(c.clusterClientMap, id)
	}
}

// publishToMember publishes events to other servers.
func (c *Client) publishToMember(
	ctx context.Context,
	clientInfo *clusterClientInfo,
	publisherID *time.ActorID,
	event sync.DocEvent,
) error {
	docEvent, err := converter.ToDocEvent(event)
	if err != nil {
		logging.From(ctx).Error(err)
		return err
	}

	if _, err := clientInfo.client.BroadcastEvent(ctx, &api.BroadcastEventRequest{
		PublisherId: publisherID.Bytes(),
		Event:       docEvent,
	}); err != nil {
		logging.From(ctx).Error(err)
		return err
	}

	return nil
}

// putSubscriptions puts the given subscriptions in etcd.
func (c *Client) putSubscriptions(
	ctx context.Context,
	keys []key.Key,
	sub *sync.Subscription,
) error {
	cli := sub.Subscriber()
	encoded, err := cli.Marshal()
	if err != nil {
		return fmt.Errorf("marshal %s: %w", sub.ID(), err)
	}

	for _, k := range keys {
		k := path.Join(subscriptionsPath, k.String(), sub.ID())
		if _, err = c.client.Put(ctx, k, encoded); err != nil {
			logging.From(ctx).Error(err)
			return fmt.Errorf("put %s: %w", k, err)
		}
	}

	return nil
}

// pullSubscriptions pulls the subscriptions of the given document key.
func (c *Client) pullSubscriptions(
	ctx context.Context,
	k key.Key,
) ([]types.Client, error) {
	getResponse, err := c.client.Get(
		ctx,
		path.Join(subscriptionsPath, k.String()),
		clientv3.WithPrefix(),
	)
	if err != nil {
		logging.From(ctx).Error(err)
		return nil, err
	}

	var clients []types.Client
	for _, kv := range getResponse.Kvs {
		cli, err := types.NewClient(kv.Value)
		if err != nil {
			return nil, err
		}
		clients = append(clients, *cli)
	}

	return clients, nil
}

// removeSubscriptions removes the given subscription in etcd.
func (c *Client) removeSubscriptions(
	ctx context.Context,
	keys []key.Key,
	sub *sync.Subscription,
) error {
	for _, docKey := range keys {
		k := path.Join(subscriptionsPath, docKey.String(), sub.ID())
		if _, err := c.client.Delete(ctx, k); err != nil {
			logging.From(ctx).Error(err)
			return err
		}
	}

	return nil
}
