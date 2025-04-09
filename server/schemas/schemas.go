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

package schemas

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
)

// CreateSchema creates a new schema in the project.
func CreateSchema(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	name string,
	version int,
	body string,
	rules []types.Rule,
) (*types.Schema, error) {
	info, err := be.DB.CreateSchemaInfo(ctx, projectID, name, version, body, rules)
	if err != nil {
		return nil, err
	}

	return info.ToSchema(), nil
}

// GetSchema retrieves a schema by its ID.
func GetSchema(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	name string,
	version int,
) (*types.Schema, error) {
	if name == "" {
		return nil, nil
	}

	info, err := be.DB.GetSchemaInfo(ctx, projectID, name, version)
	if err != nil {
		return nil, err
	}

	return info.ToSchema(), nil
}

// ListSchemas lists all schemas in the project.
func ListSchemas(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
) ([]*types.Schema, error) {
	infos, err := be.DB.ListSchemaInfos(ctx, projectID)
	if err != nil {
		return nil, err
	}
	schemas := make([]*types.Schema, 0, len(infos))
	for _, info := range infos {
		schemas = append(schemas, info.ToSchema())
	}

	return schemas, nil
}

// RemoveSchema removes a schema by its name and version.
func RemoveSchema(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	name string,
	version int,
) error {
	return be.DB.RemoveSchemaInfo(ctx, projectID, name, version)
}
