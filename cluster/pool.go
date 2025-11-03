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
)

// ClientPool manages a pool of cluster clients for reuse.
type ClientPool struct {
	clients map[string]*Client
	opts    []Option
	mu      sync.RWMutex
}

// NewClientPool creates a new client pool.
func NewClientPool(opts ...Option) *ClientPool {
	return &ClientPool{
		clients: make(map[string]*Client),
		opts:    opts,
	}
}

// Get returns a client for the given address, creating one if it doesn't exist.
func (p *ClientPool) Get(rpcAddr string) (*Client, error) {
	// Try to get existing client with read lock
	p.mu.RLock()
	if client, ok := p.clients[rpcAddr]; ok {
		p.mu.RUnlock()
		return client, nil
	}
	p.mu.RUnlock()

	// Create new client with write lock
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if client, ok := p.clients[rpcAddr]; ok {
		return client, nil
	}

	// Create new client
	client, err := Dial(rpcAddr, p.opts...)
	if err != nil {
		return nil, err
	}

	p.clients[rpcAddr] = client
	return client, nil
}

// Remove removes a client from the pool and closes it.
func (p *ClientPool) Remove(rpcAddr string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if client, ok := p.clients[rpcAddr]; ok {
		client.Close()
		delete(p.clients, rpcAddr)
	}
}

// Close closes all clients in the pool.
func (p *ClientPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, client := range p.clients {
		client.Close()
	}
	p.clients = make(map[string]*Client)
}

// Prune removes clients that are no longer in the active nodes list.
func (p *ClientPool) Prune(addrs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	activeSet := make(map[string]bool)
	for _, addr := range addrs {
		activeSet[addr] = true
	}

	for addr, client := range p.clients {
		if !activeSet[addr] {
			client.Close()
			delete(p.clients, addr)
		}
	}
}
