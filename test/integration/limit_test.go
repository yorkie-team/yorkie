//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/resource"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocSize(t *testing.T) {
	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	t.Run("Assign doc size test", func(t *testing.T) {
		ctx := context.Background()

		sizeLimit := 10 * 1024 * 1024
		projectName := "doc-size-test"
		project, err := adminCli.CreateProject(context.Background(), projectName)
		assert.NoError(t, err)
		project, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				MaxSizePerDocument: &sizeLimit,
			},
		)
		assert.NoError(t, err)

		projectInfo, err := adminCli.GetProject(ctx, projectName)
		assert.Equal(t, sizeLimit, projectInfo.MaxSizePerDocument)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))
		assert.Equal(t, sizeLimit, doc.MaxSizeLimit)
	})

	t.Run("Update doc size test", func(t *testing.T) {
		t.Skip("TODO(raararaara): Remove this after resolving inter-node projectCache invalidataion")
		ctx := context.Background()

		sizeLimit := 100
		projectName := "doc-size-update-test"
		project, err := adminCli.CreateProject(context.Background(), projectName)
		assert.NoError(t, err)
		project, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				MaxSizePerDocument: &sizeLimit,
			},
		)
		assert.NoError(t, err)

		c1, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Close()) }()
		assert.NoError(t, c1.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, doc))
		assert.Equal(t, sizeLimit, doc.MaxSizeLimit)

		newSizeLimit := 200
		project, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				MaxSizePerDocument: &newSizeLimit,
			},
		)

		c2, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Close()) }()
		assert.NoError(t, c2.Activate(ctx))

		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2))
		assert.Equal(t, newSizeLimit, doc2.MaxSizeLimit)
	})

	t.Run("Document size limit exceed test", func(t *testing.T) {
		ctx := context.Background()

		sizeLimit := 100
		projectName := "size-limit-exceed-test"
		project, err := adminCli.CreateProject(context.Background(), projectName)
		assert.NoError(t, err)
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				MaxSizePerDocument: &sizeLimit,
			},
		)
		assert.NoError(t, err)

		projectInfo, err := adminCli.GetProject(ctx, projectName)
		assert.Equal(t, sizeLimit, projectInfo.MaxSizePerDocument)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("text")
			return nil
		}))
		docSize := doc.DocSize()
		assert.Equal(t, 72, docSize.Total())

		err = doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "helloworld")
			return nil
		})
		assert.ErrorIs(t, document.ErrDocumentSizeExceedsLimit, err)
		docSize = doc.DocSize() // Data: 20, Meta: 96
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			assert.Equal(t, `{"text":[]}`, r.Marshal())
			return nil
		}))
		assert.Equal(t, 72, docSize.Total())
	})

	t.Run("Remote changes exceeding limit test", func(t *testing.T) {
		ctx := context.Background()

		sizeLimit := 100
		projectName := "remote-changes-test"
		project, err := adminCli.CreateProject(context.Background(), projectName)
		assert.NoError(t, err)
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				MaxSizePerDocument: &sizeLimit,
			},
		)
		assert.NoError(t, err)

		c1, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Close()) }()
		assert.NoError(t, c1.Activate(ctx))

		doc1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, doc1))

		c2, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Close()) }()
		assert.NoError(t, c2.Activate(ctx))

		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2))

		// Client 1 creates content (below limit)
		assert.NoError(t, doc1.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("text")
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		docSize := doc1.DocSize()
		assert.Equal(t, 72, docSize.Total())

		err = doc1.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "aa")
			return nil
		})
		docSize = doc1.DocSize()
		assert.Equal(t, 4, docSize.Live.Data)
		assert.Equal(t, 96, docSize.Live.Meta)
		assert.NoError(t, c1.Sync(ctx))

		// Client 2 adds more content that would exceed limit locally
		assert.NoError(t, doc2.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "a")
			return nil
		}))
		doc2Size := doc2.DocSize()
		assert.Equal(t, 2, doc2Size.Live.Data)
		assert.Equal(t, 96, doc2Size.Live.Meta)
		assert.NoError(t, c2.Sync(ctx))
		doc2Size = doc2.DocSize()
		// Pulls changes - should succeed despite exceeding limit
		assert.Equal(t, 6, doc2Size.Live.Data)
		assert.Equal(t, 120, doc2Size.Live.Meta)

		assert.NoError(t, c1.Sync(ctx))
		docSize = doc1.DocSize()
		assert.Equal(t, 6, docSize.Live.Data)
		assert.Equal(t, 120, docSize.Live.Meta)

		// Local update should still be restricted
		err = doc1.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "a")
			return nil
		})
		assert.ErrorIs(t, document.ErrDocumentSizeExceedsLimit, err)
		docSize = doc1.DocSize()
		assert.Equal(t, 6, docSize.Live.Data)
		assert.Equal(t, 120, docSize.Live.Meta)
	})

	t.Run("Document size consistency between document and admin API test", func(t *testing.T) {
		ctx := context.Background()

		sizeLimit := 10 * 1024 * 1024
		projectName := "ensure-size-test"
		project, err := adminCli.CreateProject(context.Background(), projectName)
		assert.NoError(t, err)
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				MaxSizePerDocument: &sizeLimit,
			},
		)
		assert.NoError(t, err)

		c1, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Close()) }()
		assert.NoError(t, c1.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, doc))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewText("text")
			return nil
		}))

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "helloworld")
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		docSize := doc.DocSize()
		assert.Equal(t, resource.DataSize{Data: 20, Meta: 96}, docSize.Live)
		assert.Equal(t, docSize, doc.CloneRoot().DocSize())

		docSummary, err := adminCli.GetDocument(
			ctx,
			projectName,
			helper.TestDocKey(t).String(),
		)
		assert.Equal(t, docSize, docSummary.DocSize)

		c2, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Close()) }()
		assert.NoError(t, c2.Activate(ctx))

		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2))
		doc2Size := doc2.DocSize()
		assert.Equal(t, doc2Size, doc2.CloneRoot().DocSize())
		assert.Equal(t, docSize, doc2Size)
	})
}
