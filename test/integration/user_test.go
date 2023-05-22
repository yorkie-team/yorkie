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

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/test/helper"
)

func TestUser(t *testing.T) {
	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { assert.NoError(t, adminCli.Close()) }()

	t.Run("user test", func(t *testing.T) {
		ctx := context.Background()
		username := "test_name"
		password := "password123!"

		_, err := adminCli.LogIn(ctx, username, password)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())

		_, err = adminCli.SignUp(ctx, "name !@#", password)
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = adminCli.SignUp(ctx, username, "pass")
		assert.Equal(t, codes.InvalidArgument, status.Convert(err).Code())

		_, err = adminCli.SignUp(ctx, username, password)
		assert.NoError(t, err)

		_, err = adminCli.LogIn(ctx, username, "asdf")
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
	})
}
