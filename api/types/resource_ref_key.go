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
	"errors"
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// DocRefKey represents an identifier used to reference a document.
type DocRefKey struct {
	Key key.Key
	ID  ID
}

// String returns the string representation of the given DocRefKey.
func (r *DocRefKey) String() string {
	return fmt.Sprintf("Doc (%s.%s)", r.Key, r.ID)
}

// Set parses the given string (format: `{docKey},{docID}`) and assigns the values
// to the given DocRefKey.
func (r *DocRefKey) Set(v string) error {
	parsed := strings.Split(v, ",")
	if len(parsed) != 2 {
		return errors.New("use the format 'docKey,docID' for the input")
	}
	r.Key = key.Key(parsed[0])
	r.ID = ID(parsed[1])
	return nil
}

// Type returns the type string of the given DocRefKey, used in cli help text.
func (r *DocRefKey) Type() string {
	return "DocumentRefKey"
}

// ClientRefKey represents an identifier used to reference a client.
type ClientRefKey struct {
	Key string
	ID  ID
}

// String returns the string representation of the given ClientRefKey.
func (r *ClientRefKey) String() string {
	return fmt.Sprintf("Client (%s.%s)", r.Key, r.ID)
}
