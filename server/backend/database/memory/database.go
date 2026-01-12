/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

// Package memory implements the database interface using in-memory database.
package memory

import (
	"context"
	"fmt"
	"sort"
	gotime "time"

	"github.com/hashicorp/go-memdb"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// DB is an in-memory database for testing or temporarily.
type DB struct {
	db *memdb.MemDB
}

// New returns a new in-memory database.
func New() (*DB, error) {
	memDB, err := memdb.NewMemDB(schema)

	if err != nil {
		return nil, fmt.Errorf("new memdb: %w", err)
	}

	return &DB{
		db: memDB,
	}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return nil
}

// clusterNodeRecord wraps ClusterNodeInfo with an ID for memory database storage.
type clusterNodeRecord struct {
	ID                        string `json:"id"`
	*database.ClusterNodeInfo `json:"info"`
}

// TryLeadership attempts to acquire or renew leadership with the given lease duration.
// If leaseToken is empty, it attempts to acquire new leadership.
// If leaseToken is provided, it attempts to renew the existing lease.
func (d *DB) TryLeadership(
	_ context.Context,
	rpcAddr string,
	leaseToken string,
	leaseDuration gotime.Duration,
) (*database.ClusterNodeInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	now := gotime.Now()
	expiresAt := now.Add(leaseDuration)

	// Find existing leadership
	it, err := txn.Get(tblClusterNodes, "is_leader", true)
	if err != nil {
		return nil, fmt.Errorf("find leadership: %w", err)
	}

	raw := it.Next()

	var existing *database.ClusterNodeInfo
	if raw != nil {
		existing = raw.(*clusterNodeRecord).ClusterNodeInfo.DeepCopy()
	}

	if leaseToken == "" {
		// Attempting to acquire new leadership
		if existing != nil {
			// Check if current leadership has expired
			if existing.ExpiresAt.After(now) {
				if err = d.updateClusterFollower(txn, rpcAddr); err != nil {
					return nil, err
				}
				txn.Commit()
			}
			return nil, nil
		}

		// Generate new lease token
		newToken, err := database.GenerateLeaseToken()
		if err != nil {
			return nil, fmt.Errorf("generate lease token: %w", err)
		}

		// Create or update leadership entry
		newLeadership := &database.ClusterNodeInfo{
			RPCAddr:    rpcAddr,
			LeaseToken: newToken,
			ExpiresAt:  expiresAt,
			UpdatedAt:  now,
			IsLeader:   true,
		}

		record := &clusterNodeRecord{
			ID:              rpcAddr,
			ClusterNodeInfo: newLeadership,
		}

		if err := txn.Insert(tblClusterNodes, record); err != nil {
			return nil, fmt.Errorf("insert clusternode of %s: %w", rpcAddr, err)
		}

		txn.Commit()
		return newLeadership, nil
	}

	// Attempting to renew existing leadership
	if existing == nil {
		return nil, database.ErrInvalidLeaseToken
	}

	// Validate the lease token
	if existing.LeaseToken != leaseToken {
		return nil, database.ErrInvalidLeaseToken
	}

	// Check if the node is the current leader
	if existing.RPCAddr != rpcAddr {
		return nil, database.ErrInvalidLeaseToken
	}

	// Check if leadership has expired
	if existing.ExpiresAt.Before(now) {
		return nil, database.ErrInvalidLeaseToken
	}

	// Generate new lease token for renewal
	newToken, err := database.GenerateLeaseToken()
	if err != nil {
		return nil, fmt.Errorf("generate lease token: %w", err)
	}

	// Update with new token and expiry
	renewedLeadership := &database.ClusterNodeInfo{
		RPCAddr:    existing.RPCAddr,
		LeaseToken: newToken,
		ExpiresAt:  expiresAt,
		UpdatedAt:  now,
		IsLeader:   true,
	}

	record := &clusterNodeRecord{
		ID:              rpcAddr,
		ClusterNodeInfo: renewedLeadership,
	}

	if err := txn.Insert(tblClusterNodes, record); err != nil {
		return nil, fmt.Errorf("renew leadership: %w", err)
	}

	txn.Commit()
	return renewedLeadership, nil
}

// FindClusterNodes returns all cluster nodes that have been updated within the given time window.
// Results are sorted with the leader first, then by updated_at descending.
func (d *DB) FindClusterNodes(
	_ context.Context,
	window gotime.Duration,
) ([]*database.ClusterNodeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClusterNodes, "id")
	if err != nil {
		return nil, fmt.Errorf("find cluster nodes: %w", err)
	}

	cutoff := gotime.Now().Add(-window)
	var infos []*database.ClusterNodeInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*clusterNodeRecord).ClusterNodeInfo
		if info.UpdatedAt.IsZero() || info.UpdatedAt.Before(cutoff) {
			continue
		}

		infos = append(infos, info.DeepCopy())
	}

	sort.Slice(infos, func(i, j int) bool {
		if infos[i].IsLeader != infos[j].IsLeader {
			return infos[i].IsLeader && !infos[j].IsLeader
		}
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})

	return infos, nil
}

// RemoveClusterNode removes the cluster node identified by rpcAddr.
func (d *DB) RemoveClusterNode(_ context.Context, rpcAddr string) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClusterNodes, "rpc_addr", rpcAddr)
	if err != nil {
		return fmt.Errorf("remove cluster node of %s: %w", rpcAddr, err)
	}
	if raw == nil {
		return fmt.Errorf("remove cluster node of %s: %w", rpcAddr, database.ErrDocumentNotFound)
	}

	if err = txn.Delete(tblClusterNodes, raw.(*clusterNodeRecord)); err != nil {
		return fmt.Errorf("remove cluster node of %s: %w", rpcAddr, err)
	}

	txn.Commit()
	return nil
}

// RemoveClusterNodes removes all cluster nodes.
func (d *DB) RemoveClusterNodes(_ context.Context) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	_, err := txn.DeleteAll(tblClusterNodes, "id")
	if err != nil {
		return fmt.Errorf("remove cluster nodes: %w", err)
	}

	txn.Commit()
	return nil
}

// updateClusterFollower updates or creates a follower node entry for the given rpcAddr.
func (d *DB) updateClusterFollower(txn *memdb.Txn, rpcAddr string) error {
	now := gotime.Now()

	n := &database.ClusterNodeInfo{
		RPCAddr:   rpcAddr,
		IsLeader:  false,
		UpdatedAt: now,
	}
	record := &clusterNodeRecord{
		ID:              rpcAddr,
		ClusterNodeInfo: n,
	}

	if err := txn.Insert(tblClusterNodes, record); err != nil {
		return fmt.Errorf("update cluster follower of %s: %w", rpcAddr, err)
	}

	return nil
}

// FindProjectInfoByPublicKey returns a project by public key.
func (d *DB) FindProjectInfoByPublicKey(
	_ context.Context,
	publicKey string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "public_key", publicKey)
	if err != nil {
		return nil, fmt.Errorf("find project by public key: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", publicKey, database.ErrProjectNotFound)
	}

	return raw.(*database.ProjectInfo).DeepCopy(), nil
}

// FindProjectInfoBySecretKey returns a project by secret key.
func (d *DB) FindProjectInfoBySecretKey(
	_ context.Context,
	secretKey string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "secret_key", secretKey)
	if err != nil {
		return nil, fmt.Errorf("find project by secret key: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", secretKey, database.ErrProjectNotFound)
	}

	return raw.(*database.ProjectInfo).DeepCopy(), nil
}

// FindProjectInfoByName returns a project by the given name.
func (d *DB) FindProjectInfoByName(
	_ context.Context,
	name string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "name", name)
	if err != nil {
		return nil, fmt.Errorf("find project by name: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", name, database.ErrProjectNotFound)
	}

	info := raw.(*database.ProjectInfo).DeepCopy()
	return info, nil
}

// FindProjectInfoByID returns a project by the given id.
func (d *DB) FindProjectInfoByID(_ context.Context, id types.ID) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	raw, err := txn.First(tblProjects, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	return raw.(*database.ProjectInfo).DeepCopy(), nil
}

// EnsureDefaultUserAndProject creates the default user and project if they do not exist.
func (d *DB) EnsureDefaultUserAndProject(
	ctx context.Context,
	username,
	password string,
) (*database.UserInfo, *database.ProjectInfo, error) {
	user, err := d.ensureDefaultUserInfo(ctx, username, password)
	if err != nil {
		return nil, nil, err
	}

	project, err := d.ensureDefaultProjectInfo(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}

	return user, project, nil
}

// ensureDefaultUserInfo creates the default user if it does not exist.
func (d *DB) ensureDefaultUserInfo(
	_ context.Context,
	username,
	password string,
) (*database.UserInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return nil, fmt.Errorf("find user by username: %w", err)
	}

	var info *database.UserInfo
	if raw == nil {
		hashedPassword, err := database.HashedPassword(password)
		if err != nil {
			return nil, err
		}
		info = database.NewUserInfo(username, hashedPassword)
		info.ID = newID()
		if err := txn.Insert(tblUsers, info); err != nil {
			return nil, fmt.Errorf("insert user: %w", err)
		}
	} else {
		info = raw.(*database.UserInfo).DeepCopy()
	}

	txn.Commit()
	return info, nil
}

// ensureDefaultProjectInfo creates the default project if it does not exist.
func (d *DB) ensureDefaultProjectInfo(
	_ context.Context,
	defaultUserID types.ID,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", database.DefaultProjectID.String())
	if err != nil {
		return nil, fmt.Errorf("find default project: %w", err)
	}

	var info *database.ProjectInfo
	if raw == nil {
		info = database.NewProjectInfo(database.DefaultProjectName, defaultUserID)
		info.ID = database.DefaultProjectID
		if err := txn.Insert(tblProjects, info); err != nil {
			return nil, fmt.Errorf("insert project: %w", err)
		}
	} else {
		info = raw.(*database.ProjectInfo).DeepCopy()
	}

	txn.Commit()
	return info, nil
}

// CreateProjectInfo creates a new project.
func (d *DB) CreateProjectInfo(
	_ context.Context,
	name string,
	owner types.ID,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// NOTE(hackerwins): Check if the project already exists.
	// https://github.com/hashicorp/go-memdb/issues/7#issuecomment-270427642
	existing, err := txn.First(tblProjects, "name", name)
	if err != nil {
		return nil, fmt.Errorf("find project by name: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("%s: %w", name, database.ErrProjectAlreadyExists)
	}

	info := database.NewProjectInfo(name, owner)
	info.ID = newID()
	if err := txn.Insert(tblProjects, info); err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}
	txn.Commit()

	return info, nil
}

// ListProjectInfos returns all project infos owned by owner.
func (d *DB) ListProjectInfos(
	_ context.Context,
	owner types.ID,
) ([]*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(
		tblProjects,
		"owner_name",
		owner.String(),
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("fetch projects by owner and name: %w", err)
	}

	var infos []*database.ProjectInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ProjectInfo).DeepCopy()
		if info.Owner == owner {
			infos = append(infos, info)
		}
	}

	return infos, nil
}

// UpdateProjectInfo updates the given project.
func (d *DB) UpdateProjectInfo(
	_ context.Context,
	id types.ID,
	fields *types.UpdatableProjectFields,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	info := raw.(*database.ProjectInfo).DeepCopy()

	if fields.Name != nil {
		existing, err := txn.First(tblProjects, "name", *fields.Name)
		if err != nil {
			return nil, fmt.Errorf("find project by name: %w", err)
		}
		if existing != nil && info.Name != *fields.Name {
			return nil, fmt.Errorf("%s: %w", *fields.Name, database.ErrProjectAlreadyExists)
		}
	}

	info.UpdateFields(fields)
	info.UpdatedAt = gotime.Now()
	if err := txn.Insert(tblProjects, info); err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}
	txn.Commit()

	return info, nil
}

// RotateProjectKeys rotates the API keys of the project.
func (d *DB) RotateProjectKeys(
	_ context.Context,
	id types.ID,
	publicKey string,
	secretKey string,
) (*database.ProjectInfo, *database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// Find project by ID
	raw, err := txn.First(tblProjects, "id", id.String())
	if err != nil {
		return nil, nil, fmt.Errorf("rotate project keys of %s: %w", id, err)
	}
	if raw == nil {
		return nil, nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	prev := raw.(*database.ProjectInfo).DeepCopy()
	info := prev.DeepCopy()

	// Update project keys
	info.PublicKey = publicKey
	info.SecretKey = secretKey
	info.UpdatedAt = gotime.Now()

	// Save updated project
	if err := txn.Insert(tblProjects, info); err != nil {
		return nil, nil, fmt.Errorf("rotate project keys of %s: %w", id, err)
	}

	txn.Commit()
	return info, prev, nil
}

// CreateUserInfo creates a new user.
func (d *DB) CreateUserInfo(
	_ context.Context,
	username string,
	hashedPassword string,
) (*database.UserInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	existing, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return nil, fmt.Errorf("create user %s: %w", username, err)
	}
	if existing != nil {
		return nil, fmt.Errorf("create user %s: %w", username, database.ErrUserAlreadyExists)
	}

	info := database.NewUserInfo(username, hashedPassword)
	info.ID = newID()
	if err := txn.Insert(tblUsers, info); err != nil {
		return nil, fmt.Errorf("create user %s: %w", username, err)
	}
	txn.Commit()

	return info, nil
}

// GetOrCreateUserInfoByGitHubID returns a user by the given GitHub ID.
func (d *DB) GetOrCreateUserInfoByGitHubID(
	_ context.Context,
	githubID string,
) (*database.UserInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", githubID)
	if err != nil {
		return nil, fmt.Errorf("find user %s: %w", githubID, err)
	}

	now := gotime.Now()
	var info *database.UserInfo

	if raw == nil {
		info = &database.UserInfo{
			ID:           newID(),
			Username:     githubID,
			AuthProvider: "github",
			CreatedAt:    now,
			AccessedAt:   now,
		}
		if err := txn.Insert(tblUsers, info); err != nil {
			return nil, fmt.Errorf("create user %s: %w", githubID, err)
		}
	} else {
		info = raw.(*database.UserInfo).DeepCopy()
		info.AccessedAt = now
		if err := txn.Insert(tblUsers, info); err != nil {
			return nil, fmt.Errorf("update user %s: %w", githubID, err)
		}
	}

	txn.Commit()
	return info, nil
}

// DeleteUserInfoByName deletes a user by name.
func (d *DB) DeleteUserInfoByName(_ context.Context, username string) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return fmt.Errorf("find user by username: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", username, database.ErrUserNotFound)
	}

	info := raw.(*database.UserInfo).DeepCopy()
	if err = txn.Delete(tblUsers, info); err != nil {
		return fmt.Errorf("delete user %s: %w", username, err)
	}

	txn.Commit()
	return nil
}

// ChangeUserPassword changes to new password.
func (d *DB) ChangeUserPassword(_ context.Context, username, hashedNewPassword string) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return fmt.Errorf("change password of %s: %w", username, err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", username, database.ErrUserNotFound)
	}

	info := raw.(*database.UserInfo).DeepCopy()
	info.HashedPassword = hashedNewPassword
	if err := txn.Insert(tblUsers, info); err != nil {
		return fmt.Errorf("change password %s: %w", username, err)
	}

	txn.Commit()

	return nil
}

// FindUserInfoByID finds a user by the given ID.
func (d *DB) FindUserInfoByID(_ context.Context, userID types.ID) (*database.UserInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "id", userID.String())
	if err != nil {
		return nil, fmt.Errorf("find user %s: %w", userID, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("find user %s: %w", userID, database.ErrUserNotFound)
	}

	return raw.(*database.UserInfo).DeepCopy(), nil
}

// FindUserInfoByName finds a user by the given username.
func (d *DB) FindUserInfoByName(_ context.Context, username string) (*database.UserInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return nil, fmt.Errorf("find user %s: %w", username, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("find user %s: %w", username, database.ErrUserNotFound)
	}

	return raw.(*database.UserInfo).DeepCopy(), nil
}

// ListUserInfos returns all users.
func (d *DB) ListUserInfos(_ context.Context) ([]*database.UserInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblUsers, "id")
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	var infos []*database.UserInfo
	for {
		raw := iter.Next()
		if raw == nil {
			break
		}
		infos = append(infos, raw.(*database.UserInfo).DeepCopy())
	}

	return infos, nil
}

// CreateMemberInfo creates a new project member.
func (d *DB) CreateMemberInfo(
	_ context.Context,
	projectID types.ID,
	userID types.ID,
	invitedBy types.ID,
	role database.MemberRole,
) (*database.MemberInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// Check if member already exists
	existing, err := txn.First(tblMembers, "project_id_user_id", projectID.String(), userID.String())
	if err != nil {
		return nil, fmt.Errorf("create project member: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("create project member: %w", database.ErrMemberAlreadyExists)
	}

	info, err := database.NewMemberInfo(projectID, userID, invitedBy, role)
	if err != nil {
		return nil, err
	}
	info.ID = newID()
	if err := txn.Insert(tblMembers, info); err != nil {
		return nil, fmt.Errorf("insert project member: %w", err)
	}
	txn.Commit()

	return info, nil
}

// ListMemberInfos returns all members of the project.
func (d *DB) ListMemberInfos(
	_ context.Context,
	projectID types.ID,
) ([]*database.MemberInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblMembers, "project_id", projectID.String())
	if err != nil {
		return nil, fmt.Errorf("list project members: %w", err)
	}

	var infos []*database.MemberInfo
	for {
		raw := iter.Next()
		if raw == nil {
			break
		}
		infos = append(infos, raw.(*database.MemberInfo).DeepCopy())
	}

	return infos, nil
}

// FindMemberInfo finds a member of the project.
func (d *DB) FindMemberInfo(
	_ context.Context,
	projectID types.ID,
	userID types.ID,
) (*database.MemberInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblMembers, "project_id_user_id", projectID.String(), userID.String())
	if err != nil {
		return nil, fmt.Errorf("find project member: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("find project member: %w", database.ErrMemberNotFound)
	}

	return raw.(*database.MemberInfo).DeepCopy(), nil
}

// UpdateMemberRole updates the role of a project member.
func (d *DB) UpdateMemberRole(
	_ context.Context,
	projectID types.ID,
	userID types.ID,
	role database.MemberRole,
) (*database.MemberInfo, error) {
	if err := role.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblMembers, "project_id_user_id", projectID.String(), userID.String())
	if err != nil {
		return nil, fmt.Errorf("update project member role: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("update project member role: %w", database.ErrMemberNotFound)
	}

	info := raw.(*database.MemberInfo).DeepCopy()
	info.Role = role

	if err := txn.Insert(tblMembers, info); err != nil {
		return nil, fmt.Errorf("update project member role: %w", err)
	}
	txn.Commit()

	return info, nil
}

// DeleteMemberInfo deletes a member from the project.
func (d *DB) DeleteMemberInfo(
	_ context.Context,
	projectID types.ID,
	userID types.ID,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblMembers, "project_id_user_id", projectID.String(), userID.String())
	if err != nil {
		return fmt.Errorf("delete project member: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("delete project member: %w", database.ErrMemberNotFound)
	}

	if err := txn.Delete(tblMembers, raw); err != nil {
		return fmt.Errorf("delete project member: %w", err)
	}
	txn.Commit()

	return nil
}

// CreateInviteInfo creates a new reusable invite link for the project.
func (d *DB) CreateInviteInfo(
	_ context.Context,
	projectID types.ID,
	token string,
	role database.MemberRole,
	createdBy types.ID,
	expiresAt *gotime.Time,
) (*database.InviteInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// Check if invite already exists (token unique).
	existing, err := txn.First(tblInvites, "token", token)
	if err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	if existing != nil {
		return nil, database.ErrInviteAlreadyExists
	}

	info, err := database.NewInviteInfo(projectID, token, role, createdBy, expiresAt)
	if err != nil {
		return nil, err
	}

	info.ID = newID()
	if err := txn.Insert(tblInvites, info); err != nil {
		return nil, fmt.Errorf("insert invite: %w", err)
	}
	txn.Commit()

	return info, nil
}

// FindInviteInfoByToken finds an invite by token.
func (d *DB) FindInviteInfoByToken(_ context.Context, token string) (*database.InviteInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblInvites, "token", token)
	if err != nil {
		return nil, fmt.Errorf("find invite: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("find invite: %w", database.ErrInviteNotFound)
	}

	return raw.(*database.InviteInfo).DeepCopy(), nil
}

// DeleteExpiredInviteInfos deletes expired invites and returns the number of deleted items.
func (d *DB) DeleteExpiredInviteInfos(_ context.Context, now gotime.Time) (int64, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	iter, err := txn.Get(tblInvites, "id")
	if err != nil {
		return 0, fmt.Errorf("delete expired invites: %w", err)
	}

	var deleted int64
	for {
		raw := iter.Next()
		if raw == nil {
			break
		}
		invite := raw.(*database.InviteInfo)
		if invite.ExpiresAt == nil {
			continue
		}
		if invite.ExpiresAt.After(now) {
			continue
		}
		if err := txn.Delete(tblInvites, raw); err != nil {
			return 0, fmt.Errorf("delete expired invites: %w", err)
		}
		deleted++
	}

	txn.Commit()
	return deleted, nil
}

// ListProjectInfosByMember returns all projects that the user is a member of.
func (d *DB) ListProjectInfosByMember(
	_ context.Context,
	userID types.ID,
) ([]*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	// Get all project memberships for the user
	iter, err := txn.Get(tblMembers, "user_id", userID.String())
	if err != nil {
		return nil, fmt.Errorf("list projects by member: %w", err)
	}

	var infos []*database.ProjectInfo
	for {
		raw := iter.Next()
		if raw == nil {
			break
		}
		memberInfo := raw.(*database.MemberInfo)

		// Get the project info
		projectRaw, err := txn.First(tblProjects, "id", memberInfo.ProjectID.String())
		if err != nil {
			return nil, fmt.Errorf("find project: %w", err)
		}
		if projectRaw != nil {
			infos = append(infos, projectRaw.(*database.ProjectInfo).DeepCopy())
		}
	}

	return infos, nil
}

// ActivateClient activates a client.
func (d *DB) ActivateClient(
	_ context.Context,
	projectID types.ID,
	key string,
	metadata map[string]string,
) (*database.ClientInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	now := gotime.Now()

	clientInfo := &database.ClientInfo{
		ID:        types.NewID(),
		ProjectID: projectID,
		Key:       key,
		Metadata:  metadata,
		Status:    database.ClientActivated,
		Documents: make(database.ClientDocInfoMap),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, fmt.Errorf("insert client: %w", err)
	}

	txn.Commit()
	return clientInfo.DeepCopy(), nil
}

// TryAttaching updates the status of the document to Attaching to prevent
// deactivating the client while the document is being attached.
func (d *DB) TryAttaching(_ context.Context, refKey types.ClientRefKey, docID types.ID) (*database.ClientInfo, error) {
	if err := refKey.ClientID.Validate(); err != nil {
		return nil, err
	}
	if err := docID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", refKey.ClientID.String())
	if err != nil {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, err)
	}

	if raw == nil {
		return nil, fmt.Errorf("%s: %w", refKey.ClientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(refKey.ProjectID); err != nil {
		return nil, err
	}

	// Check if client is activated
	if clientInfo.Status != database.ClientActivated {
		return nil, fmt.Errorf(
			"try attaching %s to %s: %w",
			docID, refKey.ClientID, database.ErrClientNotFound,
		)
	}

	// Check if document is not already attached
	if clientInfo.Documents != nil &&
		clientInfo.Documents[docID] != nil &&
		clientInfo.Documents[docID].Status == database.DocumentAttached {
		return nil, fmt.Errorf(
			"try attaching %s to %s: %w",
			docID, refKey.ClientID, database.ErrClientNotFound,
		)
	}

	// DeepCopy to avoid modifying the original object
	clientInfo = clientInfo.DeepCopy()

	// Set document to attaching state
	if clientInfo.Documents == nil {
		clientInfo.Documents = make(map[types.ID]*database.ClientDocInfo)
	}

	clientInfo.Documents[docID] = &database.ClientDocInfo{
		Status:    database.DocumentAttaching,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	clientInfo.UpdatedAt = gotime.Now()

	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, err)
	}

	txn.Commit()
	return clientInfo.DeepCopy(), nil
}

// DeactivateClient deactivates a client.
func (d *DB) DeactivateClient(_ context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	if err := refKey.ClientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", refKey.ClientID.String())
	if err != nil {
		return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, err)
	}

	if raw == nil {
		return nil, fmt.Errorf("%s: %w", refKey.ClientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(refKey.ProjectID); err != nil {
		return nil, err
	}

	// Check if client is not already deactivated
	if clientInfo.Status == database.ClientDeactivated {
		return nil, fmt.Errorf(
			"deactivate client of %s: already deactivated: %w",
			refKey.ClientID,
			database.ErrClientNotFound,
		)
	}

	// Check if any document is currently attaching or attached
	for _, docInfo := range clientInfo.Documents {
		if docInfo.Status == database.DocumentAttaching ||
			docInfo.Status == database.DocumentAttached {
			return nil, fmt.Errorf(
				"deactivate client of %s: has attached documents: %w",
				refKey.ClientID,
				database.ErrClientNotFound,
			)
		}
	}

	// NOTE(hackerwins): When retrieving objects from go-memdb, references to
	// the stored objects are returned instead of new objects. This can cause
	// problems when directly modifying loaded objects. So, we need to DeepCopy.
	clientInfo = clientInfo.DeepCopy()
	clientInfo.Deactivate()

	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, err)
	}

	txn.Commit()
	return clientInfo, nil
}

// FindClientInfoByRefKey finds a client by the given refKey.
func (d *DB) FindClientInfoByRefKey(
	_ context.Context,
	refKey types.ClientRefKey,
	skipCache ...bool,
) (*database.ClientInfo, error) {
	if err := refKey.ClientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", refKey.ClientID.String())
	if err != nil {
		return nil, fmt.Errorf("find client of %s: %w", refKey, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", refKey.ClientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(refKey.ProjectID); err != nil {
		return nil, err
	}

	return clientInfo.DeepCopy(), nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (d *DB) UpdateClientInfoAfterPushPull(
	_ context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	docRefKey := docInfo.RefKey()
	clientDocInfo := clientInfo.Documents[docRefKey.DocID]
	attached, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", clientInfo.ID.String())
	if err != nil {
		return fmt.Errorf("update client of %s after PushPull %s: %w", clientInfo.ID, docInfo.ID, err)
	}
	if raw == nil {
		return fmt.Errorf("update client of %s after PushPull %s: %w", clientInfo.ID, docInfo.ID, database.ErrClientNotFound)
	}

	loaded := raw.(*database.ClientInfo).DeepCopy()

	if !attached {
		loaded.Documents[docRefKey.DocID] = &database.ClientDocInfo{
			Status: clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	} else {
		if _, ok := loaded.Documents[docRefKey.DocID]; !ok {
			loaded.Documents[docRefKey.DocID] = &database.ClientDocInfo{}
		}

		loadedClientDocInfo := loaded.Documents[docRefKey.DocID]
		serverSeq := max(clientDocInfo.ServerSeq, loadedClientDocInfo.ServerSeq)
		clientSeq := max(clientDocInfo.ClientSeq, loadedClientDocInfo.ClientSeq)
		loaded.Documents[docRefKey.DocID] = &database.ClientDocInfo{
			ServerSeq: serverSeq,
			ClientSeq: clientSeq,
			Status:    clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	}

	if err := txn.Insert(tblClients, loaded); err != nil {
		return fmt.Errorf("update client of %s after PushPull %s: %w", clientInfo.ID, docInfo.ID, err)
	}
	txn.Commit()

	return nil
}

// FindActiveClients finds active clients for deactivation checking.
func (d *DB) FindActiveClients(
	ctx context.Context,
	candidatesLimit int,
	lastClientID types.ID,
) ([]*database.ClientInfo, types.ID, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(tblClients, "id", lastClientID.String())
	if err != nil {
		return nil, database.ZeroID, fmt.Errorf("find active clients: %w", err)
	}

	var infos []*database.ClientInfo
	var lastID types.ID = lastClientID
	count := 0
	for raw := iter.Next(); raw != nil && count < candidatesLimit; raw = iter.Next() {
		info := raw.(*database.ClientInfo)

		// Skip the starting point
		if info.ID == lastClientID {
			continue
		}

		// Always update lastID to ensure progress
		lastID = info.ID

		// Only include activated clients in results
		if info.Status != database.ClientActivated {
			continue
		}

		infos = append(infos, info.DeepCopy())
		count++
	}

	return infos, lastID, nil
}

// FindCompactionCandidates finds documents that need compaction.
func (d *DB) FindCompactionCandidates(
	ctx context.Context,
	candidatesLimit int,
	compactionMinChanges int,
	lastDocID types.ID,
) ([]*database.DocInfo, types.ID, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(tblDocuments, "id", lastDocID.String())
	if err != nil {
		return nil, database.ZeroID, fmt.Errorf("find compaction candidates direct: %w", err)
	}

	var infos []*database.DocInfo
	var lastID types.ID = lastDocID
	count := 0

	for raw := iter.Next(); raw != nil && count < candidatesLimit; raw = iter.Next() {
		info := raw.(*database.DocInfo)

		// Skip the lastDocID itself
		if info.ID == lastDocID {
			continue
		}

		// Always update lastID to ensure progress
		lastID = info.ID

		// Check if document has enough changes to compact
		if info.ServerSeq < int64(compactionMinChanges) {
			continue
		}

		// Check if document is attached to any client
		isAttached, err := d.IsDocumentAttachedOrAttaching(ctx, types.DocRefKey{
			ProjectID: info.ProjectID,
			DocID:     info.ID,
		}, types.ID(""))
		if err != nil {
			continue
		}
		if isAttached {
			continue
		}

		infos = append(infos, info.DeepCopy())
		count++
	}

	return infos, lastID, nil
}

// FindAttachedClientInfosByRefKey returns the attached client infos of the given document.
func (d *DB) FindAttachedClientInfosByRefKey(
	_ context.Context,
	docRefKey types.DocRefKey,
) ([]*database.ClientInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClients, "project_id", docRefKey.ProjectID.String())
	if err != nil {
		return nil, fmt.Errorf("find attached clients of %s: %w", docRefKey, err)
	}

	var infos []*database.ClientInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ClientInfo)

		if info.Documents[docRefKey.DocID] != nil && info.Documents[docRefKey.DocID].Status == database.DocumentAttached {
			infos = append(infos, info)
		}
	}
	return infos, nil
}

// FindAttachedClientCountsByDocIDs returns the number of attached clients of the given documents as a map.
func (d *DB) FindAttachedClientCountsByDocIDs(
	ctx context.Context,
	projectID types.ID,
	docIDs []types.ID,
) (map[types.ID]int, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClients, "project_id", projectID.String())
	if err != nil {
		return nil, fmt.Errorf("find attached client counts of %s: %w", docIDs, err)
	}

	var attachedClientMap = make(map[types.ID]int)
	for _, docID := range docIDs {
		attachedClientMap[docID] = 0
	}
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ClientInfo)
		for _, docID := range docIDs {
			if info.Documents[docID] != nil && info.Documents[docID].Status == database.DocumentAttached {
				attachedClientMap[docID]++
			}
		}
	}
	return attachedClientMap, nil
}

// FindOrCreateDocInfo finds the document or creates it if it does not exist.
func (d *DB) FindOrCreateDocInfo(
	_ context.Context,
	clientRefKey types.ClientRefKey,
	docKey key.Key,
) (*database.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	info, err := d.findDocInfoByKey(txn, clientRefKey.ProjectID, docKey)
	if err != nil {
		return info, err
	}

	if info == nil {
		now := gotime.Now()
		info = &database.DocInfo{
			ID:         newID(),
			ProjectID:  clientRefKey.ProjectID,
			Key:        docKey,
			Owner:      clientRefKey.ClientID,
			ServerSeq:  0,
			Schema:     "",
			CreatedAt:  now,
			UpdatedAt:  now,
			AccessedAt: now,
		}
		if err := txn.Insert(tblDocuments, info); err != nil {
			return nil, fmt.Errorf("find or create document of %s: %w", docKey, err)
		}
		txn.Commit()
	}

	return info.DeepCopy(), nil
}

// findDocInfoByKey finds the document of the given key.
func (d *DB) findDocInfoByKey(txn *memdb.Txn, projectID types.ID, docKey key.Key) (*database.DocInfo, error) {
	// TODO(hackerwins): Removed documents should be filtered out by the query, but
	// somehow it does not work. This is a workaround.
	// val, err := txn.First(tblDocuments, "project_id_key_removed_at", projectID.String(), key.String(), gotime.Time{})
	iter, err := txn.Get(
		tblDocuments,
		"project_id_key_removed_at",
		projectID.String(),
		docKey.String(),
		gotime.Time{},
	)
	if err != nil {
		return nil, fmt.Errorf("find document of %s: %w", docKey, err)
	}
	var docInfo *database.DocInfo
	for val := iter.Next(); val != nil; val = iter.Next() {
		if info := val.(*database.DocInfo); info.RemovedAt.IsZero() {
			docInfo = info
		}
	}

	return docInfo, nil
}

// findDocInfoByID finds the document of the given id.
func (d *DB) findDocInfoByID(txn *memdb.Txn, projectID types.ID, docID types.ID) (*database.DocInfo, error) {
	iter, err := txn.Get(
		tblDocuments,
		"project_id_id",
		projectID.String(),
		docID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("find document of %s: %w", docID, err)
	}
	var docInfo *database.DocInfo
	for val := iter.Next(); val != nil; val = iter.Next() {
		if info := val.(*database.DocInfo); info.RemovedAt.IsZero() {
			docInfo = info
		}
	}

	return docInfo, nil
}

// FindDocInfoByKey finds the document of the given key.
func (d *DB) FindDocInfoByKey(
	_ context.Context,
	projectID types.ID,
	key key.Key,
) (*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	info, err := d.findDocInfoByKey(txn, projectID, key)
	if err != nil {
		return nil, fmt.Errorf("find document of %s: %w", key, err)
	}
	if info == nil {
		return nil, fmt.Errorf("%s: %w", key, database.ErrDocumentNotFound)
	}

	return info.DeepCopy(), nil
}

// FindDocInfosByKeys finds the documents of the given keys.
func (d *DB) FindDocInfosByKeys(
	_ context.Context,
	projectID types.ID,
	keys []key.Key,
) ([]*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var infos []*database.DocInfo
	for _, k := range keys {
		info, err := d.findDocInfoByKey(txn, projectID, k)
		if err != nil {
			return nil, fmt.Errorf("find documents of %v: %w", keys, err)
		}
		if info == nil {
			continue
		}

		infos = append(infos, info.DeepCopy())
	}

	return infos, nil
}

// FindDocInfosByIDs finds the documents of the given ids.
func (d *DB) FindDocInfosByIDs(
	ctx context.Context,
	projectID types.ID,
	docIDs []types.ID,
) ([]*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var infos []*database.DocInfo
	for _, id := range docIDs {
		info, err := d.findDocInfoByID(txn, projectID, id)
		if err != nil {
			return nil, fmt.Errorf("find documents of %v: %w", docIDs, err)
		}
		if info == nil {
			continue
		}

		infos = append(infos, info.DeepCopy())
	}

	return infos, nil
}

// FindDocInfoByRefKey finds a docInfo of the given refKey.
func (d *DB) FindDocInfoByRefKey(
	_ context.Context,
	refKey types.DocRefKey,
) (*database.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return nil, fmt.Errorf("find document of %s: %w", refKey, err)
	}

	if raw == nil {
		return nil, fmt.Errorf("find document of %s: %w", refKey, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != refKey.ProjectID {
		return nil, fmt.Errorf("find document of %s: %w", refKey.DocID, database.ErrDocumentNotFound)
	}

	return docInfo.DeepCopy(), nil
}

// UpdateDocInfoStatusToRemoved updates the status of the document to removed.
func (d *DB) UpdateDocInfoStatusToRemoved(
	_ context.Context,
	refKey types.DocRefKey,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return fmt.Errorf("update %s to removed: %w", refKey, err)
	}

	if raw == nil {
		return fmt.Errorf("update %s to removed: %w", refKey.DocID, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != refKey.ProjectID {
		return fmt.Errorf("update %s to removed: %w", refKey.DocID, database.ErrDocumentNotFound)
	}

	docInfo.RemovedAt = gotime.Now()

	if err := txn.Delete(tblDocuments, docInfo); err != nil {
		return fmt.Errorf("update %s to removed: %w", refKey, err)
	}
	if err := txn.Insert(tblDocuments, docInfo); err != nil {
		return fmt.Errorf("update %s to removed: %w", refKey, err)
	}

	txn.Commit()

	return nil
}

// UpdateDocInfoSchema updates the document schema.
func (d *DB) UpdateDocInfoSchema(
	_ context.Context,
	refKey types.DocRefKey,
	schemaKey string,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return fmt.Errorf("update schema %s of %s: %w", schemaKey, refKey, err)
	}

	if raw == nil {
		return fmt.Errorf("update schema %s of %s: %w", schemaKey, refKey, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo).DeepCopy()
	if docInfo.ProjectID != refKey.ProjectID {
		return fmt.Errorf("update schema %s of %s: %w", schemaKey, refKey, database.ErrDocumentNotFound)
	}

	docInfo.Schema = schemaKey

	if err := txn.Insert(tblDocuments, docInfo); err != nil {
		return fmt.Errorf("update schema %s of %s: %w", schemaKey, refKey, database.ErrDocumentNotFound)
	}

	txn.Commit()
	return nil
}

// GetDocumentsCount returns the number of documents in the given project.
func (d *DB) GetDocumentsCount(
	_ context.Context,
	projectID types.ID,
) (int64, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblDocuments, "project_id", projectID.String())
	if err != nil {
		return 0, fmt.Errorf("count documents of %s: %w", projectID, err)
	}

	count := int64(0)
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.DocInfo).DeepCopy()
		if !info.RemovedAt.IsZero() {
			continue
		}
		count++
	}

	return count, nil
}

// GetClientsCount returns the number of active clients in the given project.
func (d *DB) GetClientsCount(ctx context.Context, projectID types.ID) (int64, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClients, "project_id", projectID.String())
	if err != nil {
		return 0, fmt.Errorf("count clients of %s: %w", projectID, err)
	}

	count := int64(0)
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ClientInfo).DeepCopy()
		if info.Status != database.ClientActivated {
			continue
		}
		count++
	}

	return count, nil
}

// CreateChangeInfos stores the given changes and doc info. If the
// removeDoc condition is true, mark IsRemoved to true in doc info.
func (d *DB) CreateChangeInfos(
	ctx context.Context,
	refKey types.DocRefKey,
	checkpoint change.Checkpoint,
	changes []*database.ChangeInfo,
	isRemoved bool,
) (*database.DocInfo, change.Checkpoint, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, err)
	}
	if raw == nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo).DeepCopy()
	if docInfo.ProjectID != refKey.ProjectID {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, database.ErrDocumentNotFound)
	}
	initialServerSeq := docInfo.ServerSeq

	for _, cn := range changes {
		serverSeq := docInfo.IncreaseServerSeq()
		checkpoint = checkpoint.NextServerSeq(serverSeq)
		cn.ServerSeq = serverSeq
		checkpoint = checkpoint.SyncClientSeq(cn.ClientSeq)

		if err := txn.Insert(tblChanges, &database.ChangeInfo{
			ID:             newID(),
			ProjectID:      docInfo.ProjectID,
			DocID:          docInfo.ID,
			ServerSeq:      cn.ServerSeq,
			ClientSeq:      cn.ClientSeq,
			Lamport:        cn.Lamport,
			ActorID:        cn.ActorID,
			VersionVector:  cn.VersionVector,
			Message:        cn.Message,
			Operations:     cn.Operations,
			PresenceChange: cn.PresenceChange,
		}); err != nil {
			return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, err)
		}
	}

	raw, err = txn.First(
		tblDocuments,
		"project_id_id",
		docInfo.ProjectID.String(),
		docInfo.ID.String(),
	)
	if err != nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, err)
	}
	if raw == nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, database.ErrDocumentNotFound)
	}
	loadedDocInfo := raw.(*database.DocInfo).DeepCopy()
	if loadedDocInfo.ServerSeq != initialServerSeq {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, database.ErrConflictOnUpdate)
	}

	now := gotime.Now()
	loadedDocInfo.ServerSeq = docInfo.ServerSeq

	for _, cn := range changes {
		if len(cn.Operations) > 0 {
			loadedDocInfo.UpdatedAt = now
			break
		}
	}

	if isRemoved {
		loadedDocInfo.RemovedAt = now
	}
	if err := txn.Insert(tblDocuments, loadedDocInfo); err != nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("create changes of %s: %w", refKey, err)
	}
	txn.Commit()

	if isRemoved {
		docInfo.RemovedAt = now
	}

	return docInfo, checkpoint, nil
}

// CompactChangeInfos stores the given compacted changes then updates the docInfo.
func (d *DB) CompactChangeInfos(
	ctx context.Context,
	docInfo *database.DocInfo,
	lastServerSeq int64,
	changes []*change.Change,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// 1. Purge the resources of the document.
	if _, err := d.purgeDocumentInternals(ctx, docInfo.ProjectID, docInfo.ID, txn); err != nil {
		return err
	}

	// 2. Store compacted change and update document
	raw, err := txn.First(
		tblDocuments,
		"project_id_id",
		docInfo.ProjectID.String(),
		docInfo.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), err)
	}
	if raw == nil {
		return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), database.ErrDocumentNotFound)
	}
	loadedDocInfo := raw.(*database.DocInfo).DeepCopy()
	if loadedDocInfo.ServerSeq != lastServerSeq {
		return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), database.ErrConflictOnUpdate)
	}

	if len(changes) == 0 {
		loadedDocInfo.ServerSeq = 0
	} else if len(changes) == 1 {
		loadedDocInfo.ServerSeq = 1
	} else {
		return fmt.Errorf("compact document of %s: invalid changes size %d", docInfo.RefKey(), len(changes))
	}

	for _, cn := range changes {
		encodedOperations, err := database.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}

		if err := txn.Insert(tblChanges, &database.ChangeInfo{
			ID:             newID(),
			ProjectID:      docInfo.ProjectID,
			DocID:          docInfo.ID,
			ServerSeq:      loadedDocInfo.ServerSeq,
			ClientSeq:      cn.ClientSeq(),
			Lamport:        cn.ID().Lamport(),
			ActorID:        types.ID(cn.ID().ActorID().String()),
			VersionVector:  cn.ID().VersionVector(),
			Message:        cn.Message(),
			Operations:     encodedOperations,
			PresenceChange: cn.PresenceChange(),
		}); err != nil {
			return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), err)
		}
	}

	// 3. Update document
	now := gotime.Now()
	loadedDocInfo.CompactedAt = now
	if err := txn.Insert(tblDocuments, loadedDocInfo); err != nil {
		return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), err)
	}

	txn.Commit()
	return nil
}

// FindLatestChangeInfoByActor returns the latest change created by given actorID.
func (d *DB) FindLatestChangeInfoByActor(
	_ context.Context,
	docRefKey types.DocRefKey,
	actorID types.ID,
	serverSeq int64,
) (*database.ChangeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.ReverseLowerBound(
		tblChanges,
		"doc_id_actor_id_server_seq",
		docRefKey.DocID.String(),
		actorID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("find the last change of %s: %w", actorID, err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ChangeInfo)
		if info != nil && info.ActorID == actorID {
			return info, nil
		}
	}

	return nil, database.ErrChangeNotFound
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (d *DB) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*change.Change, error) {
	infos, err := d.FindChangeInfosBetweenServerSeqs(ctx, docRefKey, from, to)
	if err != nil {
		return nil, err
	}

	var changes []*change.Change
	for _, info := range infos {
		c, err := info.ToChange()
		if err != nil {
			return nil, err
		}

		changes = append(changes, c)
	}

	return changes, nil
}

// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
func (d *DB) FindChangeInfosBetweenServerSeqs(
	_ context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	if from > to {
		return nil, nil
	}
	var infos []*database.ChangeInfo

	iterator, err := txn.LowerBound(
		tblChanges,
		"doc_id_server_seq",
		docRefKey.DocID.String(),
		from,
	)
	if err != nil {
		return nil, fmt.Errorf("find changes of %s: %w", docRefKey, err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ChangeInfo)
		if info.DocID != docRefKey.DocID || info.ServerSeq > to {
			break
		}
		infos = append(infos, info.DeepCopy())
	}
	return infos, nil
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (d *DB) CreateSnapshotInfo(
	_ context.Context,
	docRefKey types.DocRefKey,
	doc *document.InternalDocument,
) error {
	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	if err := txn.Insert(tblSnapshots, &database.SnapshotInfo{
		ID:            newID(),
		ProjectID:     docRefKey.ProjectID,
		DocID:         docRefKey.DocID,
		ServerSeq:     doc.Checkpoint().ServerSeq,
		Lamport:       doc.Lamport(),
		VersionVector: doc.VersionVector().DeepCopy(),
		Snapshot:      snapshot,
		CreatedAt:     gotime.Now(),
	}); err != nil {
		return fmt.Errorf("create snapshot of %s: %w", docRefKey, err)
	}
	txn.Commit()
	return nil
}

// FindSnapshotInfo returns the snapshot info of the given DocRefKey and serverSeq.
func (d *DB) FindSnapshotInfo(
	_ context.Context,
	docKey types.DocRefKey,
	serverSeq int64,
) (*database.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	raw, err := txn.First(tblSnapshots, "doc_id_server_seq",
		docKey.DocID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("find snapshot by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", docKey, database.ErrSnapshotNotFound)
	}

	return raw.(*database.SnapshotInfo).DeepCopy(), nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (d *DB) FindClosestSnapshotInfo(
	_ context.Context,
	docRefKey types.DocRefKey,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.ReverseLowerBound(
		tblSnapshots,
		"doc_id_server_seq",
		docRefKey.DocID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("find snapshot before %d of %s: %w", serverSeq, docRefKey, err)
	}

	var snapshotInfo *database.SnapshotInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.SnapshotInfo)
		if info.DocID == docRefKey.DocID {
			snapshotInfo = &database.SnapshotInfo{
				ID:            info.ID,
				ProjectID:     info.ProjectID,
				DocID:         info.DocID,
				ServerSeq:     info.ServerSeq,
				Lamport:       info.Lamport,
				VersionVector: info.VersionVector,
				CreatedAt:     info.CreatedAt,
			}
			if includeSnapshot {
				snapshotInfo.Snapshot = info.Snapshot
			}
			break
		}
	}

	if snapshotInfo == nil {
		return &database.SnapshotInfo{
			VersionVector: time.NewVersionVector(),
		}, nil
	}

	return snapshotInfo, nil
}

// updateVersionVector updates the given serverSeq of the given client
func (d *DB) updateVersionVector(
	_ context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	versionVector time.VersionVector,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	isAttached, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	if !isAttached {
		if _, err = txn.DeleteAll(
			tblVersionVectors,
			"doc_id_client_id",
			docRefKey.DocID.String(),
			clientInfo.ID.String(),
		); err != nil {
			return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
		}

		txn.Commit()
		return nil
	}

	raw, err := txn.First(
		tblVersionVectors,
		"doc_id_client_id",
		docRefKey.DocID.String(),
		clientInfo.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
	}

	versionVectorInfo := &database.VersionVectorInfo{
		DocID:         docRefKey.DocID,
		ClientID:      clientInfo.ID,
		VersionVector: versionVector,
	}
	if raw == nil {
		versionVectorInfo.ID = newID()
	} else {
		versionVectorInfo.ID = raw.(*database.VersionVectorInfo).ID
	}

	if err := txn.Insert(tblVersionVectors, versionVectorInfo); err != nil {
		return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
	}

	txn.Commit()

	return nil
}

// UpdateMinVersionVector updates the version vector of the given client
// and returns the minimum version vector of all clients.
func (d *DB) UpdateMinVersionVector(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	// 01. Update synced version vector of the given client and document.
	// NOTE(hackerwins): Considering removing the detached client's lamport
	// from the other clients' version vectors. For now, we just ignore it.
	if err := d.updateVersionVector(ctx, clientInfo, docRefKey, vector); err != nil {
		return nil, err
	}

	// 02. Compute min version vector.
	return d.GetMinVersionVector(ctx, docRefKey, vector)
}

// GetMinVersionVector returns the minimum version vector of the given document.
func (d *DB) GetMinVersionVector(
	_ context.Context,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	iterator, err := txn.Get(tblVersionVectors, "doc_id", docRefKey.DocID.String())
	if err != nil {
		return nil, fmt.Errorf("find min version vector: %w", err)
	}

	var infos []database.VersionVectorInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		vvi := raw.(*database.VersionVectorInfo)
		infos = append(infos, *vvi)
	}

	var vectors []time.VersionVector
	vectors = append(vectors, vector)
	for _, vv := range infos {
		vectors = append(vectors, vv.VersionVector)
	}

	return time.MinVersionVector(vectors...), nil
}

// FindDocInfosByPaging returns the documentInfos of the given paging.
func (d *DB) FindDocInfosByPaging(
	_ context.Context,
	projectID types.ID,
	paging types.Paging[types.ID],
) ([]*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var iterator memdb.ResultIterator
	var err error
	if paging.IsForward {
		iterator, err = txn.LowerBound(
			tblDocuments,
			"project_id_id",
			projectID.String(),
			paging.Offset.String(),
		)
	} else {
		offset := paging.Offset
		if paging.Offset == "" {
			offset = types.IDFromActorID(time.MaxActorID)
		}

		iterator, err = txn.ReverseLowerBound(
			tblDocuments,
			"project_id_id",
			projectID.String(),
			offset.String(),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("find documents of %s: %w", projectID, err)
	}

	var docInfos []*database.DocInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.DocInfo)
		// NOTE(raararaara): Unlike MongoDB, which treats PageSize == 0 as "no limit",
		// memDB requires explicit handling. If PageSize == 0, do not apply any limit.
		if paging.PageSize > 0 && len(docInfos) >= paging.PageSize {
			break
		}

		if info.ProjectID != projectID {
			break
		}

		if info.ID != paging.Offset && info.RemovedAt.IsZero() {
			docInfos = append(docInfos, info)
		}
	}

	return docInfos, nil
}

// FindDocInfosByQuery returns the docInfos which match the given query.
func (d *DB) FindDocInfosByQuery(
	_ context.Context,
	projectID types.ID,
	query string,
	pageSize int,
) (*types.SearchResult[*database.DocInfo], error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.Get(tblDocuments, "project_id_key_prefix", projectID.String(), query)
	if err != nil {
		return nil, fmt.Errorf("find documents by query %s: %w", query, err)
	}

	var docInfos []*database.DocInfo
	count := 0
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		if count < pageSize {
			info := raw.(*database.DocInfo)
			if info.IsRemoved() {
				continue
			}
			docInfos = append(docInfos, info)
		}
		count++
	}

	return &types.SearchResult[*database.DocInfo]{
		TotalCount: count,
		Elements:   docInfos,
	}, nil
}

// IsDocumentAttachedOrAttaching returns whether the document is attached or attaching to clients.
func (d *DB) IsDocumentAttachedOrAttaching(
	_ context.Context,
	refKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	it, err := txn.Get(tblClients, "project_id", refKey.ProjectID.String())
	if err != nil {
		return false, fmt.Errorf("%w", err)
	}
	if it == nil {
		return false, database.ErrClientNotFound
	}

	for raw := it.Next(); raw != nil; raw = it.Next() {
		clientInfo := raw.(*database.ClientInfo)
		if clientInfo.ID == excludeClientID {
			continue
		}
		clientDocInfo := clientInfo.Documents[refKey.DocID]
		if clientDocInfo == nil {
			continue
		}
		if clientDocInfo.Status == database.DocumentAttached ||
			clientDocInfo.Status == database.DocumentAttaching {
			return true, nil
		}
	}

	return false, nil
}

func (d *DB) CreateSchemaInfo(
	_ context.Context,
	projectID types.ID,
	name string,
	version int,
	body string,
	rules []types.Rule,
) (*database.SchemaInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// NOTE(hackerwins): Check if the project already exists.
	// https://github.com/hashicorp/go-memdb/issues/7#issuecomment-270427642
	existing, err := txn.First(
		tblSchemas,
		"project_id_name_version",
		projectID.String(),
		name,
		version,
	)
	if err != nil {
		return nil, fmt.Errorf("create schema of %s: %w", name, err)
	}
	if existing != nil {
		return nil, fmt.Errorf("create schema of %s: %w", name, database.ErrSchemaAlreadyExists)
	}

	info := &database.SchemaInfo{
		ID:        newID(),
		ProjectID: projectID,
		Name:      name,
		Version:   version,
		Body:      body,
		Rules:     rules,
		CreatedAt: gotime.Now(),
	}
	if err := txn.Insert(tblSchemas, info); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	txn.Commit()
	return info, nil
}

func (d *DB) GetSchemaInfo(
	_ context.Context,
	projectID types.ID,
	name string,
	version int,
) (*database.SchemaInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(
		tblSchemas,
		"project_id_name_version",
		projectID.String(),
		name,
		version,
	)
	if err != nil {
		return nil, fmt.Errorf("find schema of %s@%d: %w", name, version, err)
	}
	if raw == nil {
		return nil, fmt.Errorf("find schema of %s@%d: %w", name, version, database.ErrSchemaNotFound)
	}

	return raw.(*database.SchemaInfo).DeepCopy(), nil
}

func (d *DB) GetSchemaInfos(
	_ context.Context,
	projectID types.ID,
	name string,
) ([]*database.SchemaInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(
		tblSchemas,
		"project_id_name",
		projectID.String(),
		name,
	)
	if err != nil {
		return nil, fmt.Errorf("find schema: %w", err)
	}
	var infos []*database.SchemaInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		infos = append(infos, raw.(*database.SchemaInfo).DeepCopy())
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Version > infos[j].Version
	})

	if len(infos) == 0 {
		return nil, fmt.Errorf("find schemas of %s: %w", name, database.ErrSchemaNotFound)
	}
	return infos, nil
}

func (d *DB) ListSchemaInfos(
	_ context.Context,
	projectID types.ID,
) ([]*database.SchemaInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblSchemas, "project_id", projectID.String())
	if err != nil {
		return nil, fmt.Errorf("list schemas of %s: %w", projectID, err)
	}

	schemaMap := make(map[string]*database.SchemaInfo)
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		schema := raw.(*database.SchemaInfo)
		if existing, ok := schemaMap[schema.Name]; !ok || schema.Version > existing.Version {
			schemaMap[schema.Name] = schema.DeepCopy()
		}
	}

	var infos []*database.SchemaInfo
	for _, schema := range schemaMap {
		infos = append(infos, schema)
	}

	return infos, nil
}

func (d *DB) RemoveSchemaInfo(
	ctx context.Context,
	projectID types.ID,
	name string,
	version int,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(
		tblSchemas,
		"project_id_name_version",
		projectID.String(),
		name,
		version,
	)
	if err != nil {
		return fmt.Errorf("remove schema %s@%d: %w", name, version, err)
	}
	if raw == nil {
		return fmt.Errorf("remove schema %s@%d: %w", name, version, database.ErrSchemaNotFound)
	}

	schemaInfo := raw.(*database.SchemaInfo)
	if err := txn.Delete(tblSchemas, schemaInfo); err != nil {
		return fmt.Errorf("remove schema %s@%d: %w", name, version, err)
	}

	txn.Commit()
	return nil
}

// PurgeDocument purges the given document and its metadata from the database.
func (d *DB) PurgeDocument(
	ctx context.Context,
	docRefKey types.DocRefKey,
) (map[string]int64, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", docRefKey.DocID.String())
	if err != nil {
		return nil, fmt.Errorf("purge document of %s: %w", docRefKey, err)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != docRefKey.ProjectID {
		return nil, fmt.Errorf("purge document of %s: %w", docRefKey, database.ErrDocumentNotFound)
	}

	res, err := d.purgeDocumentInternals(ctx, docRefKey.ProjectID, docRefKey.DocID, txn)
	if err != nil {
		return nil, err
	}

	if err := txn.Delete(tblDocuments, docInfo); err != nil {
		return nil, fmt.Errorf("purge document of %s: %w", docRefKey, err)
	}

	txn.Commit()
	return res, nil
}

func (d *DB) purgeDocumentInternals(
	_ context.Context,
	_ types.ID,
	docID types.ID,
	txn *memdb.Txn,
) (map[string]int64, error) {
	counts := make(map[string]int64)

	count, err := txn.DeleteAll(tblChanges, "doc_id", docID.String())
	if err != nil {
		return nil, fmt.Errorf("purge changes of %s: %w", docID, err)
	}
	counts[tblChanges] = int64(count)

	count, err = txn.DeleteAll(tblSnapshots, "doc_id", docID.String())
	if err != nil {
		return nil, fmt.Errorf("purge snapshots of %s: %w", docID, err)
	}
	counts[tblSnapshots] = int64(count)

	count, err = txn.DeleteAll(tblVersionVectors, "doc_id", docID.String())
	if err != nil {
		return nil, fmt.Errorf("purge version vectors of %s: %w", docID, err)
	}
	counts[tblVersionVectors] = int64(count)

	return counts, nil
}

func newID() types.ID {
	return types.ID(bson.NewObjectID().Hex())
}

// IsSchemaAttached returns true if the schema is being used by any documents.
func (d *DB) IsSchemaAttached(
	_ context.Context,
	projectID types.ID,
	schema string,
) (bool, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(
		tblDocuments,
		"project_id",
		projectID.String(),
	)
	if err != nil {
		return false, fmt.Errorf("check if schema %s is attached: %w", schema, err)
	}

	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		doc := raw.(*database.DocInfo)
		if doc.Schema == schema && schema != "" {
			return true, nil
		}
	}

	return false, nil
}

// CreateRevisionInfo creates a new revision for the given document.
func (d *DB) CreateRevisionInfo(
	_ context.Context,
	docRefKey types.DocRefKey,
	label string,
	description string,
	snapshot []byte,
) (*database.RevisionInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	now := gotime.Now()
	revisionInfo := &database.RevisionInfo{
		ID:          newID(),
		ProjectID:   docRefKey.ProjectID,
		DocID:       docRefKey.DocID,
		Label:       label,
		Description: description,
		Snapshot:    snapshot,
		CreatedAt:   now,
	}

	if err := txn.Insert(tblRevisions, revisionInfo); err != nil {
		return nil, fmt.Errorf("insert revision: %w", err)
	}

	txn.Commit()
	return revisionInfo.DeepCopy(), nil
}

// FindRevisionInfosByPaging returns the revisions of the given document by paging.
func (d *DB) FindRevisionInfosByPaging(
	_ context.Context,
	docRefKey types.DocRefKey,
	paging types.Paging[int],
	includeSnapshot bool,
) ([]*database.RevisionInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(
		tblRevisions,
		"doc_id",
		docRefKey.DocID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("find revisions: %w", err)
	}

	var revisions []*database.RevisionInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		revision := raw.(*database.RevisionInfo)
		if revision.ProjectID == docRefKey.ProjectID {
			if !includeSnapshot {
				revisionCopy := *revision
				revisionCopy.Snapshot = nil
				revisions = append(revisions, &revisionCopy)
			} else {
				revisions = append(revisions, revision.DeepCopy())
			}
		}
	}

	// Sort by ID descending (newest first, since ID contains timestamp)
	sort.Slice(revisions, func(i, j int) bool {
		if paging.IsForward {
			return revisions[i].ID > revisions[j].ID
		}
		return revisions[i].ID < revisions[j].ID
	})

	// Apply paging
	start := paging.Offset
	if start > len(revisions) {
		start = len(revisions)
	}
	end := start + paging.PageSize
	if paging.PageSize == 0 || end > len(revisions) {
		end = len(revisions)
	}

	return revisions[start:end], nil
}

// FindRevisionInfoByID returns a revision by its ID.
func (d *DB) FindRevisionInfoByID(
	_ context.Context,
	revisionID types.ID,
) (*database.RevisionInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblRevisions, "id", revisionID.String())
	if err != nil {
		return nil, fmt.Errorf("find revision by id %s: %w", revisionID, err)
	}
	if raw == nil {
		return nil, database.ErrRevisionNotFound
	}

	return raw.(*database.RevisionInfo).DeepCopy(), nil
}

// FindRevisionInfoByLabel returns a revision by its label.
func (d *DB) FindRevisionInfoByLabel(
	_ context.Context,
	docRefKey types.DocRefKey,
	label string,
) (*database.RevisionInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(
		tblRevisions,
		"doc_id_label",
		docRefKey.DocID.String(),
		label,
	)
	if err != nil {
		return nil, fmt.Errorf("find revision by label %s: %w", label, err)
	}

	raw := iter.Next()
	if raw == nil {
		return nil, database.ErrRevisionNotFound
	}

	revision := raw.(*database.RevisionInfo)
	if revision.ProjectID != docRefKey.ProjectID {
		return nil, database.ErrRevisionNotFound
	}

	return revision.DeepCopy(), nil
}

// DeleteRevisionInfo deletes a revision by its ID.
func (d *DB) DeleteRevisionInfo(
	_ context.Context,
	revisionID types.ID,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblRevisions, "id", revisionID.String())
	if err != nil {
		return fmt.Errorf("find revision %s: %w", revisionID, err)
	}
	if raw == nil {
		return database.ErrRevisionNotFound
	}

	if err := txn.Delete(tblRevisions, raw); err != nil {
		return fmt.Errorf("delete revision %s: %w", revisionID, err)
	}

	txn.Commit()
	return nil
}
