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
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// SchemaInfo is a struct that represents the schema information
type SchemaInfo struct {
	// ID is the unique identifier of the schema.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project that the schema belongs to.
	ProjectID types.ID `bson:"project_id"`

	// Name is the name of the schema.
	Name string `bson:"name"`

	// Version is the version of the schema.
	Version int `bson:"version"`

	// Body is the body of the schema.
	Body string `bson:"body"`

	// Rules are the rules of the schema.
	Rules []types.Rule `bson:"rules"`

	// CreatedAt is the time when the document is created.
	CreatedAt time.Time `bson:"created_at"`
}

// ToSchema converts the SchemaInfo to a Schema.
func (s *SchemaInfo) ToSchema() *types.Schema {
	return &types.Schema{
		ID:        s.ID,
		ProjectID: s.ProjectID,
		Name:      s.Name,
		Version:   s.Version,
		Body:      s.Body,
		Rules:     s.Rules,
		CreatedAt: s.CreatedAt,
	}
}

// DeepCopy returns a deep copy of the SchemaInfo.
func (s *SchemaInfo) DeepCopy() *SchemaInfo {
	if s == nil {
		return nil
	}

	return &SchemaInfo{
		ID:        s.ID,
		ProjectID: s.ProjectID,
		Name:      s.Name,
		Version:   s.Version,
		Body:      s.Body,
		Rules:     s.Rules,
		CreatedAt: s.CreatedAt,
	}
}
