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
	"errors"
	"fmt"
	"strings"
)

const (
	// Splitter is used to separate collection and document in a string.
	Splitter = "$"
	tokenLen = 2
)

// ErrInvalidCombinedKey is returned when the given combined key is invalid.
var ErrInvalidCombinedKey = errors.New("invalid combined key")

// Key represents the key of the Document.
type Key struct {
	Collection string
	Document   string
}

// FromCombinedKey creates an instance of Key from the given combined key.
func FromCombinedKey(k string) (Key, error) {
	splits := strings.Split(k, Splitter)
	if len(splits) != tokenLen {
		return Key{}, fmt.Errorf("%s: %w", k, ErrInvalidCombinedKey)
	}

	return Key{Collection: splits[0], Document: splits[1]}, nil
}

// CombinedKey returns the string of this key.
func (k Key) CombinedKey() string {
	return k.Collection + Splitter + k.Document
}
