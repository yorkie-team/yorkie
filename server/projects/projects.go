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
	"fmt"
	"time"

	"github.com/lithammer/shortuuid/v4"
	"golang.org/x/sync/errgroup"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/authz"
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
	info, err := be.DB.CreateProjectInfo(ctx, name, owner)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// ListProjects lists all projects owned by or accessible to the user.
func ListProjects(
	ctx context.Context,
	be *backend.Backend,
	owner types.ID,
) ([]*types.Project, error) {
	// Get projects owned by the user
	ownedInfos, err := be.DB.ListProjectInfos(ctx, owner)
	if err != nil {
		return nil, err
	}

	// Get projects where the user is a member
	memberInfos, err := be.DB.ListProjectInfosByMember(ctx, owner)
	if err != nil {
		return nil, err
	}

	// Combine and deduplicate projects
	projectMap := make(map[types.ID]*types.Project)
	for _, info := range ownedInfos {
		projectMap[info.ID] = info.ToProject()
	}
	for _, info := range memberInfos {
		if _, exists := projectMap[info.ID]; !exists {
			projectMap[info.ID] = info.ToProject()
		}
	}

	var projects []*types.Project
	for _, project := range projectMap {
		projects = append(projects, project)
	}

	return projects, nil
}

// ProjectAndRole returns a project by the given name.
// It checks both ownership and membership to determine access.
// Returns the project and the user's role (owner, admin, or member).
func ProjectAndRole(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	name string,
) (*types.Project, database.MemberRole, error) {
	// Get project and user's role using the authorization helper
	projectID, role, err := authz.FindUserRoleByName(ctx, be, userID, name)
	if err != nil {
		return nil, "", err
	}

	// Get project info
	info, err := be.DB.FindProjectInfoByID(ctx, projectID)
	if err != nil {
		return nil, "", err
	}

	return info.ToProject(), role, nil
}

// UpdateProject updates a project.
// Only users with Admin or Owner role can update project settings.
func UpdateProject(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	id types.ID,
	fields *types.UpdatableProjectFields,
) (*types.Project, error) {
	// Check permission: Admin or Owner required
	if err := authz.CheckPermission(ctx, be, userID, id, database.Admin); err != nil {
		return nil, err
	}

	// Update project
	info, err := be.DB.UpdateProjectInfo(ctx, id, fields)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// GetProjectStats returns the project stats.
func GetProjectStats(
	ctx context.Context,
	be *backend.Backend,
	id types.ID,
	from time.Time,
	to time.Time,
) (*types.ProjectStats, error) {
	// NOTE(raararaara): The warehouse (StarRocks) metrics, the cached counts
	// (MongoDB), and the live channel count (cluster RPC) are independent reads.
	// They are fetched concurrently so the dashboard entry latency is bounded by
	// the slowest single read instead of the sum of all reads. errgroup cancels
	// the derived context on the first error, so a slow query is not left running
	// after the client has already given up.
	var (
		activeUsers                 []types.MetricPoint
		activeUsersCount            int
		activeDocuments             []types.MetricPoint
		activeDocumentsCount        int
		activeClients               []types.MetricPoint
		activeClientsCount          int
		activeChannels              []types.MetricPoint
		activeChannelsCount         int
		sessions                    []types.MetricPoint
		sessionsCount               int
		peakSessionsPerChannel      []types.MetricPoint
		peakSessionsPerChannelCount int
		counts                      *database.ProjectStatsCounts
		channelsCount               int
	)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		activeUsers, err = be.Warehouse.GetActiveUsers(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeUsersCount, err = be.Warehouse.GetActiveUsersCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeDocuments, err = be.Warehouse.GetActiveDocuments(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeDocumentsCount, err = be.Warehouse.GetActiveDocumentsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeClients, err = be.Warehouse.GetActiveClients(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeClientsCount, err = be.Warehouse.GetActiveClientsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeChannels, err = be.Warehouse.GetActiveChannels(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		activeChannelsCount, err = be.Warehouse.GetActiveChannelsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		sessions, err = be.Warehouse.GetSessions(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		sessionsCount, err = be.Warehouse.GetSessionsCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		peakSessionsPerChannel, err = be.Warehouse.GetPeakSessionsPerChannel(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		peakSessionsPerChannelCount, err = be.Warehouse.GetPeakSessionsPerChannelCount(ctx, id, from, to)
		return err
	})
	g.Go(func() (err error) {
		counts, err = be.DB.GetProjectStatsCounts(ctx, id)
		return err
	})
	g.Go(func() (err error) {
		channelsCount, err = be.BroadcastChannelCount(ctx, id)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("collect project stats: %w", err)
	}

	return &types.ProjectStats{
		ActiveUsersCount:            activeUsersCount,
		ActiveUsers:                 activeUsers,
		ActiveDocumentsCount:        activeDocumentsCount,
		ActiveDocuments:             activeDocuments,
		ActiveClientsCount:          activeClientsCount,
		ActiveClients:               activeClients,
		ActiveChannelsCount:         activeChannelsCount,
		ActiveChannels:              activeChannels,
		SessionsCount:               sessionsCount,
		Sessions:                    sessions,
		PeakSessionsPerChannelCount: peakSessionsPerChannelCount,
		PeakSessionsPerChannel:      peakSessionsPerChannel,
		DocumentsCount:              counts.DocumentsCount,
		ClientsCount:                counts.ClientsCount,
		ChannelsCount:               int64(channelsCount),
		StatsUpdatedAt:              counts.UpdatedAt,
	}, nil
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

// ProjectFromSecretKey returns a project from a secret key.
func ProjectFromSecretKey(ctx context.Context, be *backend.Backend, secretKey string) (*types.Project, error) {
	// NOTE(kokodak): If the secretKey is empty, fallback to the default project.
	if secretKey == "" {
		info, err := be.DB.FindProjectInfoByID(ctx, database.DefaultProjectID)
		if err != nil {
			return nil, err
		}
		return info.ToProject(), nil
	}

	info, err := be.DB.FindProjectInfoBySecretKey(ctx, secretKey)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// RotateProjectKeys rotates the API keys of the project.
// Only users with Admin or Owner role can rotate keys.
func RotateProjectKeys(
	ctx context.Context,
	be *backend.Backend,
	userID types.ID,
	id types.ID,
) (*types.Project, *types.Project, error) {
	// Check permission: Admin or Owner required
	if err := authz.CheckPermission(ctx, be, userID, id, database.Admin); err != nil {
		return nil, nil, err
	}

	// Generate new API keys
	publicKey := shortuuid.New()
	secretKey := shortuuid.New()

	// Update project with new keys
	info, prev, err := be.DB.RotateProjectKeys(ctx, id, publicKey, secretKey)
	if err != nil {
		return nil, nil, err
	}

	return info.ToProject(), prev.ToProject(), nil
}
