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

	schemaName := fmt.Sprintf("schema-%d", gotime.Now().UnixMilli())
	err = adminCli.CreateSchema(
		ctx,
		"default",
		schemaName,
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

	t.Run("attach document with schema test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))

		err = cli.Attach(ctx, doc, client.WithSchema("noexist@1"))
		assert.Error(t, err)

		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName+"@1")))
		cli.Deactivate(ctx)
	})

	t.Run("reject local update that violates schema test", func(t *testing.T) {
		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName+"@1")))

		err = doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("title", 123)
			return nil
		})
		assert.ErrorIs(t, err, document.ErrSchemaValidationFailed)

		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("title", "hello")
			return nil
		}))

		cli.Deactivate(ctx)
	})

	t.Run("delete schema test", func(t *testing.T) {
		schemaName1 := fmt.Sprintf("schema1-%d", gotime.Now().UnixMilli())
		schemaName2 := fmt.Sprintf("schema2-%d", gotime.Now().UnixMilli())
		err = adminCli.CreateSchema(
			ctx,
			"default",
			schemaName1,
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
		err = adminCli.CreateSchema(
			ctx,
			"default",
			schemaName2,
			1,
			"type Document = {title2: string;};",
			[]types.Rule{
				{
					Path: "$.title2",
					Type: "string",
				},
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(svr.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc, client.WithSchema(schemaName1+"@1")))

		err = adminCli.RemoveSchema(ctx, "default", schemaName1, 1)
		assert.Error(t, err)
		err = adminCli.RemoveSchema(ctx, "default", schemaName2, 1)
		assert.NoError(t, err)

		schemas, err := adminCli.GetSchemas(ctx, "default", schemaName1)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(schemas))
		schemas, err = adminCli.GetSchemas(ctx, "default", schemaName2)
		assert.Error(t, err)
		assert.Equal(t, 0, len(schemas))

		cli.Deactivate(ctx)
	})
}
