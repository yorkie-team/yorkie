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
	"fmt"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocumentSchema(t *testing.T) {
	ctx := context.Background()
	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	schemaName1 := fmt.Sprintf("schema1-%d", gotime.Now().UnixMilli())
	schemaName2 := fmt.Sprintf("schema2-%d", gotime.Now().UnixMilli())
	schemaRule1 := []types.Rule{
		{
			Path: "$.title",
			Type: "string",
		},
	}
	schemaRule2 := []types.Rule{
		{
			Path: "$.title2",
			Type: "string",
		},
	}
	err = adminCli.CreateSchema(
		ctx,
		"default",
		schemaName1,
		1,
		"type Document = {title: string;};",
		schemaRule1,
	)
	assert.NoError(t, err)
	err = adminCli.CreateSchema(
		ctx,
		"default",
		schemaName2,
		1,
		"type Document = {title2: string;};",
		schemaRule2,
	)
	assert.NoError(t, err)

	t.Run("attach schema test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName1+"@1")))
		assert.Equal(t, schemaRule1, doc.SchemaRules)
		cli.Deactivate(ctx)
	})

	t.Run("attach with non-existent schema test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		err = cli.Attach(ctx, doc, client.WithSchema("not-exist-schema@1"))
		assert.Error(t, err)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName1+"@1")))
		assert.Equal(t, schemaRule1, doc.SchemaRules)
	})

	t.Run("attach schema to already attached document test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))
		cli2, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli2.Close()) }()
		assert.NoError(t, cli2.Activate(ctx))

		// cli attach without schema
		doc := document.New(helper.TestDocKey(t))
		err = cli.Attach(ctx, doc)
		assert.NoError(t, err)

		// cli2 try to attach schema
		doc2 := document.New(helper.TestDocKey(t))
		err = cli2.Attach(ctx, doc2, client.WithSchema(schemaName1+"@1"))
		assert.NoError(t, err)
		assert.Equal(t, []types.Rule(nil), doc2.SchemaRules)

		// clients detach
		err = cli.Detach(ctx, doc)
		assert.NoError(t, err)
		err = cli2.Detach(ctx, doc2)
		assert.NoError(t, err)

		// cli2 attach schema after all clients detach
		doc3 := document.New(helper.TestDocKey(t))
		err = cli2.Attach(ctx, doc3, client.WithSchema(schemaName1+"@1"))
		assert.NoError(t, err)
		assert.Equal(t, schemaRule1, doc3.SchemaRules)
	})

	t.Run("attach new schema to document with existing schema test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))
		cli2, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli2.Close()) }()
		assert.NoError(t, cli2.Activate(ctx))

		// cli attach with schema1
		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName1+"@1")))
		assert.Equal(t, schemaRule1, doc.SchemaRules)

		// cli2 try to attach schema2 to document that already has schema1
		doc2 := document.New(helper.TestDocKey(t))
		err = cli2.Attach(ctx, doc2, client.WithSchema(schemaName2+"@1"))
		assert.NoError(t, err)
		assert.Equal(t, schemaRule1, doc2.SchemaRules)
	})

	t.Run("reject local update that violates schema test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName1+"@1")))

		// update with invalid schema
		err = doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("title", 123)
			return nil
		})
		assert.ErrorIs(t, err, document.ErrSchemaValidationFailed)

		// update with valid schema
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("title", "hello")
			return nil
		}))

		cli.Deactivate(ctx)
	})

	t.Run("schema sharing between clients test", func(t *testing.T) {
		cli1, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli1.Close()) }()
		assert.NoError(t, cli1.Activate(ctx))
		cli2, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli2.Close()) }()
		assert.NoError(t, cli2.Activate(ctx))

		// client1 attaches document with schema1
		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli1.Attach(ctx, doc, client.WithSchema(schemaName1+"@1")))
		assert.Equal(t, schemaRule1, doc.SchemaRules)

		// client2 attaches document
		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli2.Attach(ctx, doc2))
		assert.Equal(t, schemaRule1, doc2.SchemaRules)

		// client2 tries to update with invalid schema
		err = doc2.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("title", 123)
			return nil
		})
		assert.ErrorIs(t, err, document.ErrSchemaValidationFailed)

		// client2 updates with valid schema
		assert.NoError(t, doc2.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("title", "hello")
			return nil
		}))
		assert.Equal(t, `{"title":"hello"}`, doc2.Marshal())

		// Sync changes between clients
		assert.NoError(t, cli2.Sync(ctx))
		assert.NoError(t, cli1.Sync(ctx))
		assert.Equal(t, doc2.Marshal(), doc.Marshal())
		cli1.Deactivate(ctx)
		cli2.Deactivate(ctx)
	})

	t.Run("delete schema test", func(t *testing.T) {
		schemaName3 := fmt.Sprintf("schema3-%d", gotime.Now().UnixMilli())
		schemaName4 := fmt.Sprintf("schema4-%d", gotime.Now().UnixMilli())
		err = adminCli.CreateSchema(
			ctx,
			"default",
			schemaName3,
			1,
			"type Document = {title: string;};",
			schemaRule1,
		)
		assert.NoError(t, err)
		err = adminCli.CreateSchema(
			ctx,
			"default",
			schemaName4,
			1,
			"type Document = {title2: string;};",
			schemaRule2,
		)
		assert.NoError(t, err)

		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		// Can remove schema that is not in use
		err = adminCli.RemoveSchema(ctx, "default", schemaName3, 1)
		assert.NoError(t, err)
		schemas, err := adminCli.GetSchemas(ctx, "default", schemaName3)
		assert.Error(t, err)
		assert.Equal(t, 0, len(schemas))

		// Cannot remove schema that is attached to a document
		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName4+"@1")))
		err = adminCli.RemoveSchema(ctx, "default", schemaName4, 1)
		assert.Error(t, err)
		schemas, err = adminCli.GetSchemas(ctx, "default", schemaName4)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(schemas))

		cli.Deactivate(ctx)
	})

	t.Run("create schema test", func(t *testing.T) {
		err = adminCli.CreateSchema(
			ctx,
			"default",
			"new",
			1,
			"type Document = {title: string;};",
			[]types.Rule{
				{
					Path: "$.title",
					Type: "string",
				},
			},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "reserved_schema_name")

		err = adminCli.CreateSchema(
			ctx,
			"default",
			"new-schema",
			0,
			"type Document = {title: string;};",
			[]types.Rule{
				{
					Path: "$.title",
					Type: "string",
				},
			},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "min")

		err = adminCli.CreateSchema(
			ctx,
			"default",
			"new-schema",
			1,
			"type Document = {title: string;};",
			[]types.Rule{
				{
					Path: "$.title",
					Type: "string",
				},
			},
		)
		assert.NoError(t, err)
	})
}
