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

package database

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// ClusterNodeInfo represents the leadership information of a node in the cluster.
type ClusterNodeInfo struct {
	RPCAddr    string    `bson:"rpc_addr"`
	LeaseToken string    `bson:"lease_token"`
	ExpiresAt  time.Time `bson:"expires_at"`
	UpdatedAt  time.Time `bson:"updated_at"`
	IsLeader   bool      `bson:"is_leader"`
}

// IsExpired returns true if the leadership lease has expired.
func (ci *ClusterNodeInfo) IsExpired() bool {
	return time.Now().After(ci.ExpiresAt)
}

// TimeUntilExpiry returns the duration until the lease expires.
func (ci *ClusterNodeInfo) TimeUntilExpiry() time.Duration {
	return time.Until(ci.ExpiresAt)
}

// DeepCopy returns a deep copy of this cluster node info.
func (ci *ClusterNodeInfo) DeepCopy() *ClusterNodeInfo {
	if ci == nil {
		return nil
	}
	cpy := *ci
	return &cpy
}

// GenerateLeaseToken generates a cryptographically secure random token
func GenerateLeaseToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
