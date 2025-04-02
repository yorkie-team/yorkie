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

package types

import (
	"time"
)

// Rule is a rule that defines the structure of a document.
type Rule struct {
	Path string `json:"path"`

	Type string `json:"type"`
}

type Schema struct {
	// ID is the unique identifier of the schema.
	ID ID `json:"id"`

	// Name is the name of the schema.
	Name string `json:"name"`

	// Version is the version of the schema.
	Version int `json:"version"`

	// Body is the body of the schema.
	Body string `json:"body"`

	Rules []Rule `json:"rules"`

	// CreatedAt is the time when the document is created.
	CreatedAt time.Time `json:"created_at"`
}
