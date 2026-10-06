//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestAuthWebhookRemovalByMethod checks that the server decides whether a
// request removes the document by its method, not by the is_removed flag the
// client puts in the change pack. The project asks the webhook about
// RemoveDocument only, as an operator who lets everyone sync but gates
// removal would configure it.
func TestAuthWebhookRemovalByMethod(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	asked := map[string][]types.AccessAttribute{}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		mu.Lock()
		asked[req.Token] = append(asked[req.Token], req.Attributes...)
		mu.Unlock()

		// The reader may do anything but write the document.
		res := types.AuthWebhookResponse{Allowed: true}
		for _, attr := range req.Attributes {
			presenceOnly := attr.PresenceOnly != nil && *attr.PresenceOnly
			if req.Token == "reader" && attr.Verb == types.ReadWrite && !presenceOnly {
				res = types.AuthWebhookResponse{Allowed: false, Reason: "read-only member"}
				w.WriteHeader(http.StatusForbidden)
				break
			}
		}
		_, err = res.Write(w)
		assert.NoError(t, err)
	}))
	defer authServer.Close()

	svr, err := server.New(helper.TestConfig())
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer adminCli.Close()
	project, err := adminCli.CreateProject(ctx, "removal-by-method")
	require.NoError(t, err)
	methods := []string{string(types.RemoveDocument)}
	_, err = adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		AuthWebhookURL:     &authServer.URL,
		AuthWebhookMethods: &methods,
	})
	require.NoError(t, err)

	raw := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+svr.RPCAddr())
	withHeaders := func(req connect.AnyRequest, token string, k key.Key) {
		req.Header().Set(types.APIKeyKey, project.PublicKey)
		req.Header().Set(types.AuthorizationKey, token)
		req.Header().Set(types.ShardKey, project.PublicKey+"/"+k.String())
	}

	// attached is a raw client, acting as the given token, attached to k.
	type attached struct {
		token, clientID, docID string
		key                    key.Key
	}
	activate := func(t *testing.T, token string, k key.Key) attached {
		req := connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()})
		withHeaders(req, token, k)
		res, err := raw.ActivateClient(ctx, req)
		require.NoError(t, err)
		return attached{token: token, clientID: res.Msg.ClientId, key: k}
	}
	attach := func(t *testing.T, a *attached, isRemoved bool) {
		req := connect.NewRequest(&api.AttachDocumentRequest{
			ClientId: a.clientID,
			ChangePack: &api.ChangePack{
				DocumentKey: a.key.String(), Checkpoint: &api.Checkpoint{}, IsRemoved: isRemoved,
			},
		})
		withHeaders(req, a.token, a.key)
		res, err := raw.AttachDocument(ctx, req)
		require.NoError(t, err)
		a.docID = res.Msg.DocumentId
	}
	// emptyPack is a pack with no changes, the one a removal usually sends.
	emptyPack := func(a attached, isRemoved bool) *api.ChangePack {
		return &api.ChangePack{
			DocumentKey: a.key.String(), Checkpoint: &api.Checkpoint{}, IsRemoved: isRemoved,
		}
	}
	remove := func(a attached, isRemoved bool) error {
		req := connect.NewRequest(&api.RemoveDocumentRequest{
			ClientId: a.clientID, DocumentId: a.docID, ChangePack: emptyPack(a, isRemoved),
		})
		withHeaders(req, a.token, a.key)
		_, err := raw.RemoveDocument(ctx, req)
		return err
	}

	dial := func(t *testing.T) *client.Client {
		cli, err := client.Dial(
			svr.RPCAddr(), client.WithToken("writer"), client.WithAPIKey(project.PublicKey),
		)
		require.NoError(t, err)
		require.NoError(t, cli.Activate(ctx))
		return cli
	}
	// createDoc has a writer leave {"x":1} in the document, so a removal shows
	// as a fresh {} on the next attach.
	createDoc := func(t *testing.T, k key.Key) {
		cli := dial(t)
		defer func() { assert.NoError(t, cli.Close()) }()
		doc := document.New(k)
		require.NoError(t, cli.Attach(ctx, doc))
		require.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("x", 1)
			return nil
		}))
		require.NoError(t, cli.Detach(ctx, doc))
	}
	contentOf := func(t *testing.T, k key.Key) string {
		cli := dial(t)
		defer func() { assert.NoError(t, cli.Close()) }()
		doc := document.New(k)
		require.NoError(t, cli.Attach(ctx, doc))
		defer func() { assert.NoError(t, cli.Detach(ctx, doc)) }()
		return doc.Marshal()
	}

	t.Run("is_removed on another method removes nothing", func(t *testing.T) {
		// Each of these methods is not gated, so before the fix the flag
		// removed the document without the webhook ever being asked.
		for name, send := range map[string]func(t *testing.T, a *attached){
			"AttachDocument": func(t *testing.T, a *attached) {
				attach(t, a, true)
			},
			"PushPullChanges": func(t *testing.T, a *attached) {
				attach(t, a, false)
				req := connect.NewRequest(&api.PushPullChangesRequest{
					ClientId: a.clientID, DocumentId: a.docID, ChangePack: emptyPack(*a, true),
				})
				withHeaders(req, a.token, a.key)
				_, err := raw.PushPullChanges(ctx, req)
				require.NoError(t, err)
			},
			"DetachDocument": func(t *testing.T, a *attached) {
				attach(t, a, false)
				req := connect.NewRequest(&api.DetachDocumentRequest{
					ClientId: a.clientID, DocumentId: a.docID, ChangePack: emptyPack(*a, true),
				})
				withHeaders(req, a.token, a.key)
				_, err := raw.DetachDocument(ctx, req)
				require.NoError(t, err)
			},
		} {
			t.Run(name, func(t *testing.T) {
				k := helper.TestKey(t)
				createDoc(t, k)
				a := activate(t, "reader", k)
				send(t, &a)
				assert.Equal(t, `{"x":1}`, contentOf(t, k))
			})
		}
	})

	t.Run("RemoveDocument without is_removed is asked as a write", func(t *testing.T) {
		k := helper.TestKey(t)
		createDoc(t, k)
		a := activate(t, "reader", k)
		attach(t, &a, false)

		// Before the fix this was asked as r and let through.
		err := remove(a, false)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		no := false
		mu.Lock()
		assert.Contains(t, asked["reader"], types.AccessAttribute{
			Key: k.String(), Verb: types.ReadWrite, PresenceOnly: &no,
		})
		mu.Unlock()
		assert.Equal(t, `{"x":1}`, contentOf(t, k))
	})

	t.Run("RemoveDocument without is_removed still removes", func(t *testing.T) {
		k := helper.TestKey(t)
		createDoc(t, k)
		a := activate(t, "writer", k)
		attach(t, &a, false)

		// Before the fix this detached the client as removed but left the
		// document in place.
		require.NoError(t, remove(a, false))
		assert.Equal(t, `{}`, contentOf(t, k))
	})
}
