/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package key

import (
	"strings"

	"github.com/pkg/errors"
)

const (
	// BSONSplitter is used to separate collection and document in a string.
	BSONSplitter = "$"

	tokenLen = 2
)

// Key represents the key of the Document.
type Key struct {
	Collection string
	Document   string
}

// FromBSONKey creates an instance of Key from the received bsonKey.
func FromBSONKey(bsonKey string) (*Key, error) {
	splits := strings.Split(bsonKey, BSONSplitter)
	if len(splits) != tokenLen {
		return nil, errors.New("fail to create key from bson key")
	}

	return &Key{Collection: splits[0], Document: splits[1]}, nil
}

// BSONKey returns the string of this key.
func (k *Key) BSONKey() string {
	return k.Collection + BSONSplitter + k.Document
}
