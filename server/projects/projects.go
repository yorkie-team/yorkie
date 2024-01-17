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

// Package projects provides the project related business logic.
package projects

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// CreateProject creates a project.
func CreateProject(
	ctx context.Context,
	be *backend.Backend,
	owner types.ID,
	name string,
) (*types.Project, error) {
	info, err := be.DB.CreateProjectInfo(ctx, name, owner, be.Config.ClientDeactivateThreshold)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// ListProjects lists all projects.
func ListProjects(
	ctx context.Context,
	be *backend.Backend,
	owner types.ID,
) ([]*types.Project, error) {
	infos, err := be.DB.ListProjectInfos(ctx, owner)
	if err != nil {
		return nil, err
	}

	var projects []*types.Project
	for _, info := range infos {
		projects = append(projects, info.ToProject())
	}

	return projects, nil
}

// GetProject returns a project by the given name.
func GetProject(
	ctx context.Context,
	be *backend.Backend,
	owner types.ID,
	name string,
) (*types.Project, error) {
	info, err := be.DB.FindProjectInfoByName(ctx, owner, name)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// UpdateProject updates a project.
func UpdateProject(
	ctx context.Context,
	be *backend.Backend,
	owner types.ID,
	id types.ID,
	fields *types.UpdatableProjectFields,
) (*types.Project, error) {
	info, err := be.DB.UpdateProjectInfo(ctx, owner, id, fields)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// GetProjectFromAPIKey returns a project from an API key.
func GetProjectFromAPIKey(ctx context.Context, be *backend.Backend, apiKey string) (*types.Project, error) {
	// TODO(hackerwins): Default project without API key should be allowed only in standalone mode.
	if apiKey == "" {
		info, err := be.DB.FindProjectInfoByID(ctx, database.DefaultProjectID)
		if err != nil {
			return nil, err
		}
		return info.ToProject(), nil
	}

	info, err := be.DB.FindProjectInfoByPublicKey(ctx, apiKey)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// GetProjectFromSecretKey returns a project from a secret key.
func GetProjectFromSecretKey(ctx context.Context, be *backend.Backend, secretKey string) (*types.Project, error) {
	info, err := be.DB.FindProjectInfoBySecretKey(ctx, secretKey)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}
