//go:build integration

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

package integration

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/test/helper"
)

func TestUser(t *testing.T) {
	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { adminCli.Close() }()

	t.Run("user test", func(t *testing.T) {
		ctx := context.Background()
		username := "test_name"
		password := "password123!"

		_, err := adminCli.LogIn(ctx, username, password)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

		_, err = adminCli.SignUp(ctx, "name !@#", password)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		_, err = adminCli.SignUp(ctx, username, "pass")
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

		_, err = adminCli.SignUp(ctx, username, password)
		assert.NoError(t, err)

		_, err = adminCli.LogIn(ctx, username, "asdf")
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
