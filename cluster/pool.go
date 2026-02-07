/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package cluster provides a cluster client pool for managing connections to cluster nodes.
package cluster

import (
	"sync"
	"sync/atomic"
)

// ClientPool manages a pool of cluster clients for reuse.
// It maintains multiple connections per host to reduce HTTP/2 mutex contention.
type ClientPool struct {
	clients  map[string][]*Client
	counters map[string]*uint64
	opts     []Option
	poolSize int
	mu       sync.RWMutex
}

// NewClientPool creates a new client pool.
func NewClientPool(opts ...Option) *ClientPool {
	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	poolSize := options.PoolSize
	if poolSize <= 0 {
		poolSize = 1
	}

	return &ClientPool{
		clients:  make(map[string][]*Client),
		counters: make(map[string]*uint64),
		opts:     opts,
		poolSize: poolSize,
	}
}

// Get returns a client for the given address, creating clients if they don't exist.
// It uses round-robin selection to distribute load across multiple connections.
func (p *ClientPool) Get(rpcAddr string) (*Client, error) {
	// Try to get existing client with read lock
	p.mu.RLock()
	if clients, ok := p.clients[rpcAddr]; ok {
		counter := atomic.AddUint64(p.counters[rpcAddr], 1)
		client := clients[counter%uint64(len(clients))]
		p.mu.RUnlock()
		return client, nil
	}
	p.mu.RUnlock()

	// Create new clients with write lock
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if clients, ok := p.clients[rpcAddr]; ok {
		counter := atomic.AddUint64(p.counters[rpcAddr], 1)
		return clients[counter%uint64(len(clients))], nil
	}

	// Create pool of clients
	clients := make([]*Client, p.poolSize)
	for i := range clients {
		cli, err := Dial(rpcAddr, p.opts...)
		if err != nil {
			// Cleanup already created clients
			for j := 0; j < i; j++ {
				clients[j].Close()
			}
			return nil, err
		}
		clients[i] = cli
	}

	var counter uint64
	p.clients[rpcAddr] = clients
	p.counters[rpcAddr] = &counter
	return clients[0], nil
}

// Remove removes clients for the given address from the pool and closes them.
func (p *ClientPool) Remove(rpcAddr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if clients, ok := p.clients[rpcAddr]; ok {
		for _, client := range clients {
			client.Close()
		}
		delete(p.clients, rpcAddr)
		delete(p.counters, rpcAddr)
	}
}

// Close closes all clients in the pool.
func (p *ClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, clients := range p.clients {
		for _, client := range clients {
			client.Close()
		}
	}
	p.clients = make(map[string][]*Client)
	p.counters = make(map[string]*uint64)
}

// Prune removes clients that are no longer in the active nodes list.
func (p *ClientPool) Prune(addrs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	activeSet := make(map[string]bool)
	for _, addr := range addrs {
		activeSet[addr] = true
	}

	for addr, clients := range p.clients {
		if !activeSet[addr] {
			for _, client := range clients {
				client.Close()
			}
			delete(p.clients, addr)
			delete(p.counters, addr)
		}
	}
}
