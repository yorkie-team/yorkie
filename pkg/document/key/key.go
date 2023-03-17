/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package key provides the key implementation of the document.
package key

import (
	"errors"

	"github.com/yorkie-team/yorkie/internal/validation"
)

var (
	// ErrInvalidKey is returned when the key is invalid.
	ErrInvalidKey = errors.New("invalid key, key must be a slug with 4-120 characters")
)

// Key represents a document key.
type Key string

// String returns the string representation of the key.
func (k Key) String() string {
	return string(k)
}

// Validate checks whether the key is valid or not.
func (k Key) Validate() error {
	if err := validation.Validate(k.String(), []any{
		"required",
		"case_sensitive_slug",
		"min=4",
		"max=120",
	}); err != nil {
		return ErrInvalidKey
	}

	return nil
}
