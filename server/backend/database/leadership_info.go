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

// LeadershipInfo represents the leadership information of a node in the cluster.
type LeadershipInfo struct {
	Hostname   string    `bson:"hostname"`
	LeaseToken string    `bson:"lease_token"`
	ElectedAt  time.Time `bson:"elected_at"`
	ExpiresAt  time.Time `bson:"expires_at"`
	RenewedAt  time.Time `bson:"renewed_at"`
	Term       int64     `bson:"term"`
	Singleton  int       `bson:"singleton"`
}

// IsExpired returns true if the leadership lease has expired.
func (li *LeadershipInfo) IsExpired() bool {
	return time.Now().After(li.ExpiresAt)
}

// TimeUntilExpiry returns the duration until the lease expires.
func (li *LeadershipInfo) TimeUntilExpiry() time.Duration {
	return time.Until(li.ExpiresAt)
}

// GenerateLeaseToken generates a cryptographically secure random token
func GenerateLeaseToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
