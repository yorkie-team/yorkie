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

	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// DocRefKey represents an identifier used to reference a document.
type DocRefKey struct {
	ProjectID ID
	DocID     ID
	DocKey    key.Key
}

// String returns the string representation of the given DocRefKey.
func (r DocRefKey) String() string {
	return fmt.Sprintf("Document (%s.%s.%s)", r.ProjectID, r.DocID, r.DocKey.String())
}

// ClientRefKey represents an identifier used to reference a client.
type ClientRefKey struct {
	ProjectID ID
	ClientID  ID
	ClientKey string
}

// String returns the string representation of the given ClientRefKey.
func (r ClientRefKey) String() string {
	return fmt.Sprintf("Client (%s.%s.%s)", r.ProjectID, r.ClientID, r.ClientKey)
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
