package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.etcd.io/etcd/clientv3"
	"go.etcd.io/etcd/mvcc/mvccpb"

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

var (
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

	return c.memberMap
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
		if err := c.putAgent(context.Background()); err != nil {
			log.Logger.Error(err)
		}

		select {
		case <-time.After(putAgentPeriod):
		case <-c.closing:
			return
		}
	}
}

// putAgent puts the local agent in etcd.
func (c *Client) putAgent(ctx context.Context) error {
	grantResponse, err := c.client.Grant(ctx, int64(agentValueTTL.Seconds()))
	if err != nil {
		return fmt.Errorf("grant %s: %w", c.pubSub.AgentInfo.ID, err)
	}

	agentInfo := *c.pubSub.AgentInfo
	agentInfo.UpdatedAt = time.Now()
	bytes, err := json.Marshal(agentInfo)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", c.pubSub.AgentInfo.ID, err)
	}

	key := fmt.Sprintf("%s/%s", agentsPath, c.pubSub.AgentInfo.ID)
	_, err = c.client.Put(ctx, key, string(bytes), clientv3.WithLease(grantResponse.ID))
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// removeAgent removes the local agent in etcd.
func (c *Client) removeAgent(ctx context.Context) error {
	key := fmt.Sprintf("%s/%s", agentsPath, c.pubSub.AgentInfo.ID)
	_, err := c.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("remove %s: %w", key, err)
	}
	return nil
}

// syncAgents syncs the local member map with etcd.
func (c *Client) syncAgents() {
	watchCh := c.client.Watch(context.Background(), agentsPath, clientv3.WithPrefix())
	for {
		select {
		case watchResponse := <-watchCh:
			for _, event := range watchResponse.Events {
				switch event.Type {
				case mvccpb.PUT:
					var info sync.AgentInfo
					if err := json.Unmarshal(event.Kv.Value, &info); err != nil {
						log.Logger.Error(err)
						continue
					}
					c.setAgentInfo(string(event.Kv.Key), info)
				case mvccpb.DELETE:
					c.removeAgentInfo(string(event.Kv.Key))
				}
			}
		case <-c.closing:
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
func (c *Client) removeAgentInfo(key string) {
	c.memberMapMu.Lock()
	defer c.memberMapMu.Unlock()

	delete(c.memberMap, key)
}
