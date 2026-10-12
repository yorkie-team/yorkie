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
	"strings"
	"time"

	"github.com/lithammer/shortuuid/v4"
	"golang.org/x/sync/errgroup"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/authz"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrSecretKeyNotProvided is returned when a project is looked up by an
	// empty secret key.
	ErrSecretKeyNotProvided = errors.Unauthenticated("secret key is not provided").WithCode("ErrSecretKeyNotProvided")
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
		if _, exists := projectMap[info.ID]; exists {
			continue
		}

		// NOTE(hackerwins): ListProjectInfosByMember drops the membership row,
		// so the role has to be read back to decide whether this user may see
		// the project's secret key. The owned list above needs no such lookup:
		// being in it is the owner role.
		//
		// The read-back can fail on its own — ListProjectInfosByMember reads the
		// membership rows and the projects in two separate queries, so a
		// membership revoked in between leaves a project whose row is already
		// gone. That must not fail the whole listing, which before this lookup
		// always returned; an unresolved role is treated as no role, which
		// redacts the key and leaves the rest of the project visible.
		var role database.MemberRole
		member, err := be.DB.FindMemberInfo(ctx, info.ID, owner)
		if err != nil {
			logging.DefaultLogger().Warnf(
				"list projects: resolve member role of %s for %s: %v",
				info.ID, owner, err,
			)
		} else {
			role = member.Role
		}
		projectMap[info.ID] = redactForRole(info.ToProject(), role)
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

	return redactForRole(info.ToProject(), role), role, nil
}

// CanReadSecretKey reports whether a user holding the given role in a project
// may read the project's secret key: a known role of at least Admin.
//
// The key authenticates the project-scoped admin procedures, which have no
// user in their context and so authorize on the key alone. Handing it to a
// member would let that member act with the authority of an owner.
//
// NOTE(hackerwins): the role is validated before the comparison because
// MemberRole.IsAtLeast ranks a role it does not recognize as 0, which compares
// as more privileged than Owner. Without the guard an empty or corrupted role
// string would read as "at least Admin" and hand the key out, so this gate
// fails closed instead.
func CanReadSecretKey(role database.MemberRole) bool {
	return role.Validate() == nil && role.IsAtLeast(database.Admin)
}

// redactForRole clears the project's secret key unless the role may read it,
// before the project leaves the lookup that resolved the role.
func redactForRole(project *types.Project, role database.MemberRole) *types.Project {
	if CanReadSecretKey(role) {
		return project
	}

	return project.WithoutSecretKey()
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
		activeUsers            []types.MetricPoint
		activeUsersCount       int
		activeDocuments        []types.MetricPoint
		activeDocumentsCount   int
		activeClients          []types.MetricPoint
		activeClientsCount     int
		activeChannels         []types.MetricPoint
		activeChannelsCount    int
		sessions               []types.MetricPoint
		sessionsCount          int
		peakSessionsPerChannel []types.MetricPoint
		counts                 *database.ProjectStatsCounts
		channelsCount          int
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
		PeakSessionsPerChannelCount: maxMetricValue(peakSessionsPerChannel),
		PeakSessionsPerChannel:      peakSessionsPerChannel,
		DocumentsCount:              counts.DocumentsCount,
		ClientsCount:                counts.ClientsCount,
		ChannelsCount:               int64(channelsCount),
		StatsUpdatedAt:              counts.UpdatedAt,
	}, nil
}

// maxMetricValue returns the largest value in the series, or 0 when it is
// empty.
//
// This is how the window's peak total is derived, instead of asking the
// warehouse for it. The peak of a window is a MAX over independent
// per-(day, channel) buckets, and MAX is associative: reducing the buckets to a
// daily maximum first and then taking the maximum of those days gives exactly
// the same number as reducing all the buckets at once. Querying it separately
// only repeated the series' scan.
//
// It works for peak alone. The other metrics are distinct counts, whose window
// total is an HLL union across the days rather than the maximum, or the sum, of
// the per-day values: a subject active on several days must still count once.
func maxMetricValue(points []types.MetricPoint) int {
	maxValue := 0
	for _, p := range points {
		if p.Value > maxValue {
			maxValue = p.Value
		}
	}
	return maxValue
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
//
// NOTE(hackerwins): Unlike GetProjectFromAPIKey, an empty key does not fall
// back to the default project. The secret key is the whole credential of the
// admin path, so a missing one must not resolve to any project.
//
// The key carries no user, so this lookup has no role to check. The key stands
// in for Admin: only Admin and above may read it (redactForRole), and it is
// replaced when a user who could read it is demoted below Admin or removed
// (RotateSecretKey). A copy taken before either rule existed keeps working
// until the project's keys are rotated.
func ProjectFromSecretKey(ctx context.Context, be *backend.Backend, secretKey string) (*types.Project, error) {
	if strings.TrimSpace(secretKey) == "" {
		return nil, ErrSecretKeyNotProvided
	}

	info, err := be.DB.FindProjectInfoBySecretKey(ctx, secretKey)
	if err != nil {
		return nil, err
	}

	return info.ToProject(), nil
}

// RotateSecretKey replaces the project's secret key and keeps its public key.
//
// It revokes the key a user could read before losing the role that allowed it.
// The key carries no user, so a copy outlives the membership it was read
// under; replacing it is the only way to take that authority back. The public
// key identifies the project to SDK clients and grants none of the admin
// authority, so it stays and those clients keep working.
//
// The caller is responsible for authorization and for invalidating the project
// cache on other nodes.
func RotateSecretKey(
	ctx context.Context,
	be *backend.Backend,
	id types.ID,
) (*types.Project, error) {
	info, err := be.DB.FindProjectInfoByID(ctx, id)
	if err != nil {
		return nil, err
	}

	rotated, _, err := be.DB.RotateProjectKeys(ctx, id, info.PublicKey, shortuuid.New())
	if err != nil {
		return nil, err
	}

	return rotated.ToProject(), nil
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
