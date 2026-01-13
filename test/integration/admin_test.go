//go:build integration

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package integration

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestAdmin(t *testing.T) {
	ctx := context.Background()

	adminCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
	assert.NoError(t, err)
	_, err = adminCli.LogIn(ctx, "admin", "admin")
	assert.NoError(t, err)
	defer func() {
		adminCli.Close()
	}()

	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	t.Run("admin and client document creation sync test", func(t *testing.T) {
		initialRoot := yson.Object{
			"text": yson.Text{Nodes: []yson.TextNode{{Value: "hello"}, {Value: "world"}}},
			"Tree": yson.Tree{
				Root: yson.TreeNode{
					Type: yson.DefaultRootNodeType,
					Children: []yson.TreeNode{
						{Type: yson.TextNodeType, Value: "hello"},
						{Type: yson.TextNodeType, Value: "world"},
					},
				},
			},
		}

		_, err := adminCli.CreateDocument(ctx, "default", helper.TestKey(t).String(), initialRoot)
		if err != nil {
			assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
			return
		}

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Deactivate(ctx))
			assert.NoError(t, cli.Close())
		}()

		doc := document.New(helper.TestKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))

		actualRoot, err := yson.FromCRDT(doc.RootObject())
		assert.NoError(t, err)
		assert.Equal(t, initialRoot, actualRoot)
	})

	t.Run("admin and client document update sync test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()

		doc := document.New(helper.TestKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))

		_, err = adminCli.UpdateDocument(ctx, "default", doc.Key(),
			`{"counter": Counter(Int(0))}`, "")
		assert.NoError(t, err)
		assert.NoError(t, cli.Sync(ctx))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.Equal(t, int32(1), doc.Root().GetCounter("counter").Value())
		assert.NoError(t, cli.Sync(ctx))

		_, err = adminCli.UpdateDocument(ctx, "default", doc.Key(),
			`{"counter": Counter(Int(0))}`, "")
		assert.NoError(t, err)
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, int32(0), doc.Root().GetCounter("counter").Value())
	})

	t.Run("admin and client document deletion sync test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()

		d1 := document.New(helper.TestKey(t))

		// 01. admin tries to remove document that does not exist.
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String(), true)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		// 02. client creates a document then admin removes the document.
		assert.NoError(t, cli.Attach(ctx, d1))
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String(), true)
		assert.NoError(t, err)
		assert.Equal(t, document.StatusAttached, d1.Status())

		// 03. client updates the document and sync to the server.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, document.StatusRemoved, d1.Status())
	})

	t.Run("document event propagation on removal test", func(t *testing.T) {
		ctx := context.Background()

		// 01. c1 attaches and watches d1.
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		wg := sync.WaitGroup{}
		wg.Add(1)
		rch, cancel, err := c1.WatchStream(d1)
		assert.NoError(t, err)
		defer cancel()
		go func() {
			defer wg.Done()

			for {
				resp := <-rch
				if resp.Err == io.EOF {
					assert.Fail(t, resp.Err.Error())
					return
				}
				assert.NoError(t, resp.Err)

				if resp.Type == client.DocumentChanged {
					err := c1.Sync(ctx, client.WithKey(d1.Key()))
					assert.NoError(t, err)
					return
				}
			}
		}()

		// 02. adminCli removes d1.
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String(), true)
		assert.NoError(t, err)

		// 03. wait for watching document changed event.
		wg.Wait()
		assert.Equal(t, document.StatusRemoved, d1.Status())
	})

	t.Run("document removal without force test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()
		doc := document.New(helper.TestKey(t))

		// 01. try to remove document that does not exist.
		err = adminCli.RemoveDocument(ctx, "default", doc.Key().String(), false)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		// 02. try to remove document that is attached by the client.
		assert.NoError(t, cli.Attach(ctx, doc))
		err = adminCli.RemoveDocument(ctx, "default", doc.Key().String(), false)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		assert.Equal(t, document.StatusAttached, doc.Status())

		// 03. remove document that is detached by the client.
		assert.NoError(t, cli.Detach(ctx, doc))
		err = adminCli.RemoveDocument(ctx, "default", doc.Key().String(), false)
		assert.NoError(t, err)
		assert.Equal(t, document.StatusDetached, doc.Status())
	})

	t.Run("unauthentication test", func(t *testing.T) {
		// 01. try to call admin API without token.
		cli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
		assert.NoError(t, err)
		defer func() {
			cli.Close()
		}()
		_, err = cli.GetProject(ctx, "default")

		// 02. server should return unauthenticated error.
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}

func TestAdminMember(t *testing.T) {
	ctx := context.Background()

	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { adminCli.Close() }()

	newSlug := func(prefix string) string {
		return fmt.Sprintf("%s%s", prefix, strconv.FormatInt(time.Now().UnixNano(), 10))
	}

	newUser := func(t *testing.T) (username, password string) {
		t.Helper()
		// 01. Create a new user (signup) for member test cases.
		username = "user" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000_000, 10)
		password = "password123!"
		_, err := adminCli.SignUp(ctx, username, password)
		assert.NoError(t, err)
		return username, password
	}

	findByUsername := func(list []*types.Member, username string) (*types.Member, bool) {
		for _, m := range list {
			if m.Username == username {
				return m, true
			}
		}
		return nil, false
	}

	t.Run("Create and Accept the Invite test", func(t *testing.T) {
		// 01. Create a project.
		projectName := newSlug("prj")
		_, err := adminCli.CreateProject(ctx, projectName)
		assert.NoError(t, err)

		// 02. Create an invite link (reusable).
		token, err := adminCli.CreateInvite(
			ctx,
			projectName,
			api.InviteExpireOption_INVITE_EXPIRE_OPTION_SEVEN_DAYS,
		)
		assert.NoError(t, err)
		assert.NotEmpty(t, token)

		// 03. Create a user who will accept the invite.
		username, password := newUser(t)

		userCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
		assert.NoError(t, err)
		defer func() { userCli.Close() }()
		_, err = userCli.LogIn(ctx, username, password)
		assert.NoError(t, err)

		// 04. Accept invite.
		member, err := userCli.AcceptInvite(ctx, token)
		assert.NoError(t, err)

		// 05. Verify the user exists via ListMembers (AcceptInvite response doesn't include username in API spec).
		memberList, err := adminCli.ListMembers(ctx, projectName)
		assert.NoError(t, err)
		assert.Len(t, memberList, 2) // Owner + invited user

		owner, ok := findByUsername(memberList, server.DefaultAdminUser)
		assert.True(t, ok)
		assert.Equal(t, "owner", owner.Role)

		_, ok = findByUsername(memberList, username)
		assert.True(t, ok)

		// 06. Accepting again should be idempotent.
		member2, err := userCli.AcceptInvite(ctx, token)
		assert.NoError(t, err)
		assert.Equal(t, member.ID, member2.ID)
	})

	t.Run("ListMembers test", func(t *testing.T) {
		// 01. Create a project.
		projectName := newSlug("prj")
		project, err := adminCli.CreateProject(ctx, projectName)
		assert.NoError(t, err)

		// 02. Create invite link and accept as user.
		token, err := adminCli.CreateInvite(
			ctx,
			projectName,
			api.InviteExpireOption_INVITE_EXPIRE_OPTION_SEVEN_DAYS,
		)
		assert.NoError(t, err)

		username, password := newUser(t)
		userCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
		assert.NoError(t, err)
		defer func() { userCli.Close() }()
		_, err = userCli.LogIn(ctx, username, password)
		assert.NoError(t, err)
		_, err = userCli.AcceptInvite(ctx, token)
		assert.NoError(t, err)

		// 03. List members.
		memberList, err := adminCli.ListMembers(ctx, projectName)
		assert.NoError(t, err)
		assert.Len(t, memberList, 2)

		owner, ok := findByUsername(memberList, server.DefaultAdminUser)
		assert.True(t, ok)
		assert.Equal(t, project.ID, owner.ProjectID)
		assert.Equal(t, "owner", owner.Role)
		assert.NotEmpty(t, owner.UserID)

		// 04. Verify invited member is included.
		invited, ok := findByUsername(memberList, username)
		assert.True(t, ok)
		assert.Equal(t, project.ID, invited.ProjectID)
		assert.Equal(t, "member", invited.Role)
	})

	t.Run("RemoveMember test", func(t *testing.T) {
		// 01. Create a project.
		projectName := newSlug("prj")
		_, err := adminCli.CreateProject(ctx, projectName)
		assert.NoError(t, err)

		// 02. Create invite link and accept as user.
		token, err := adminCli.CreateInvite(
			ctx,
			projectName,
			api.InviteExpireOption_INVITE_EXPIRE_OPTION_SEVEN_DAYS,
		)
		assert.NoError(t, err)

		username, password := newUser(t)
		userCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
		assert.NoError(t, err)
		defer func() { userCli.Close() }()
		_, err = userCli.LogIn(ctx, username, password)
		assert.NoError(t, err)
		_, err = userCli.AcceptInvite(ctx, token)
		assert.NoError(t, err)

		// 03. Remove the member.
		err = adminCli.RemoveMember(ctx, projectName, username)
		assert.NoError(t, err)

		// 04. Verify the member is removed (only owner remains).
		memberList, err := adminCli.ListMembers(ctx, projectName)
		assert.NoError(t, err)
		assert.Len(t, memberList, 1)
		owner, ok := findByUsername(memberList, server.DefaultAdminUser)
		assert.True(t, ok)
		assert.Equal(t, "owner", owner.Role)
		_, ok = findByUsername(memberList, username)
		assert.False(t, ok)

		// 05. Removing the same user again should fail with NotFound.
		err = adminCli.RemoveMember(ctx, projectName, username)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("UpdateMemberRole test", func(t *testing.T) {
		// 01. Create a project.
		projectName := newSlug("prj")
		_, err := adminCli.CreateProject(ctx, projectName)
		assert.NoError(t, err)

		// 02. Create invite link and accept as user.
		token, err := adminCli.CreateInvite(
			ctx,
			projectName,
			api.InviteExpireOption_INVITE_EXPIRE_OPTION_SEVEN_DAYS,
		)
		assert.NoError(t, err)

		username, password := newUser(t)
		userCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
		assert.NoError(t, err)
		defer func() { userCli.Close() }()
		_, err = userCli.LogIn(ctx, username, password)
		assert.NoError(t, err)
		_, err = userCli.AcceptInvite(ctx, token)
		assert.NoError(t, err)

		// 03. Update member role and verify the response.
		updated, err := adminCli.UpdateMemberRole(ctx, projectName, username, "admin")
		assert.NoError(t, err)
		assert.Equal(t, username, updated.Username)
		assert.Equal(t, "admin", updated.Role)

		// 04. Verify the updated role is reflected in list members.
		memberList, err := adminCli.ListMembers(ctx, projectName)
		assert.NoError(t, err)
		assert.Len(t, memberList, 2)
		owner, ok := findByUsername(memberList, server.DefaultAdminUser)
		assert.True(t, ok)
		assert.Equal(t, "owner", owner.Role)

		invited, ok := findByUsername(memberList, username)
		assert.True(t, ok)
		assert.Equal(t, "admin", invited.Role)

		// 05. Updating role for a non-existent user should fail with NotFound.
		_, err = adminCli.UpdateMemberRole(ctx, projectName, "nonexistentuser123", "admin")
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}
