/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package types

import (
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/key"
)

// ClientRefKey represents an identifier used to reference a client.
type ClientRefKey struct {
	ProjectID ID
	ClientID  ID
}

// String returns the string representation of the given ClientRefKey.
func (r ClientRefKey) String() string {
	return fmt.Sprintf("Client (%s.%s)", r.ProjectID, r.ClientID)
}

// CacheKey returns the wire form of the given ClientRefKey, used to name the
// client in a cluster cache invalidation. Unlike String, it round-trips
// through ParseClientRefKey.
func (r ClientRefKey) CacheKey() string {
	return fmt.Sprintf("%s/%s", r.ProjectID, r.ClientID)
}

// ParseClientRefKey parses the wire form produced by ClientRefKey.CacheKey.
func ParseClientRefKey(cacheKey string) (ClientRefKey, error) {
	projectID, clientID, found := strings.Cut(cacheKey, "/")
	if !found {
		return ClientRefKey{}, fmt.Errorf("parse client ref key %q: no separator", cacheKey)
	}

	refKey := ClientRefKey{ProjectID: ID(projectID), ClientID: ID(clientID)}
	if err := refKey.ProjectID.Validate(); err != nil {
		return ClientRefKey{}, fmt.Errorf("parse client ref key %q: %w", cacheKey, err)
	}
	if err := refKey.ClientID.Validate(); err != nil {
		return ClientRefKey{}, fmt.Errorf("parse client ref key %q: %w", cacheKey, err)
	}

	return refKey, nil
}

// DocRefKey represents an identifier used to reference a document.
type DocRefKey struct {
	ProjectID ID
	DocID     ID
}

// String returns the string representation of the given DocRefKey.
func (r DocRefKey) String() string {
	return fmt.Sprintf("Document (%s.%s)", r.ProjectID, r.DocID)
}

// ChannelRefKey represents an identifier used to reference a channel.
type ChannelRefKey struct {
	ProjectID  ID
	ChannelKey key.Key
}

// String returns the string representation of the given ChannelRefKey.
func (r ChannelRefKey) String() string {
	return fmt.Sprintf("Channel (%s.%s)", r.ProjectID, r.ChannelKey)
}

// EventRefKey represents an identifier used to reference an event.
type EventRefKey struct {
	DocRefKey
	EventWebhookType
}

// String returns the string representation of the given EventRefKey.
func (r EventRefKey) String() string {
	return fmt.Sprintf("DocEvent (%s.%s.%s)", r.ProjectID, r.DocID, r.EventWebhookType)
}
