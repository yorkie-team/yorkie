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

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/api/types"
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

	projectName := "doc-size-test"
	project, err := adminCli.CreateProject(context.Background(), projectName)
	assert.NoError(t, err)

	t.Run("Assign doc size test", func(t *testing.T) {
		ctx := context.Background()
		assert.NoError(t, err)

		sizeLimit := 10 * 1024 * 1024
		_, err := adminCli.UpdateProject(
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
			client.WithToken("invalid"),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.TestDocKey(t))
		err = cli.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.Equal(t, sizeLimit, doc.MaxSizeLimit)
	})

	t.Run("", func(t *testing.T) {
		//ctx := context.Background()
		//assert.NoError(t, err)
	})
}
