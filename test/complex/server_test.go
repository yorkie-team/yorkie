//go:build complex

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

package complex

import (
	"testing"

	"github.com/yorkie-team/yorkie/server/rpc/testcases"
)

func TestSDKRPCServerBackendWithShardedDB(t *testing.T) {
	t.Run("activate/deactivate client test", func(t *testing.T) {
		testcases.RunActivateAndDeactivateClientTest(t, testClient)
	})

	t.Run("attach/detach document test", func(t *testing.T) {
		testcases.RunAttachAndDetachDocumentTest(t, testClient)
	})

	t.Run("attach/detach on removed document test", func(t *testing.T) {
		testcases.RunAttachAndDetachRemovedDocumentTest(t, testClient)
	})

	t.Run("push/pull changes test", func(t *testing.T) {
		testcases.RunPushPullChangeTest(t, testClient)
	})

	t.Run("push/pull on removed document test", func(t *testing.T) {
		testcases.RunPushPullChangeOnRemovedDocumentTest(t, testClient)
	})

	t.Run("remove document test", func(t *testing.T) {
		testcases.RunRemoveDocumentTest(t, testClient)
	})

	t.Run("remove document with invalid client state test", func(t *testing.T) {
		testcases.RunRemoveDocumentWithInvalidClientStateTest(t, testClient)
	})

	t.Run("watch document test", func(t *testing.T) {
		testcases.RunWatchDocumentTest(t, testClient)
	})
}

func TestAdminRPCServerBackendWithShardedDB(t *testing.T) {
	t.Run("admin signup test", func(t *testing.T) {
		testcases.RunAdminSignUpTest(t, testAdminClient)
	})

	t.Run("admin login test", func(t *testing.T) {
		testcases.RunAdminLoginTest(t, testAdminClient)
	})

	t.Run("admin delete account test", func(t *testing.T) {
		testcases.RunAdminDeleteAccountTest(t, testAdminClient)
	})

	t.Run("admin change password test", func(t *testing.T) {
		testcases.RunAdminChangePasswordTest(t, testAdminClient)
	})

	t.Run("admin create project test", func(t *testing.T) {
		testcases.RunAdminCreateProjectTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin list projects test", func(t *testing.T) {
		testcases.RunAdminListProjectsTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin get project test", func(t *testing.T) {
		testcases.RunAdminGetProjectTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin update project test", func(t *testing.T) {
		testcases.RunAdminUpdateProjectTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin update project cache invalidation test", func(t *testing.T) {
		testcases.RunAdminUpdateProjectCacheInvalidationTest(t, testClient, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin list documents test", func(t *testing.T) {
		testcases.RunAdminListDocumentsTest(t, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin get document test", func(t *testing.T) {
		testcases.RunAdminGetDocumentTest(t, testClient, testAdminClient, testAdminAuthInterceptor)
	})

	t.Run("admin list changes test", func(t *testing.T) {
		testcases.RunAdminListChangesTest(t, testClient, testAdminClient, testAdminAuthInterceptor)
	})
}
