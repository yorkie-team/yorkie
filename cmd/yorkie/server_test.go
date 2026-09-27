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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server"
)

func TestResolveServerConfigKeepsExplicitCacheDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	require.NoError(t, os.WriteFile(path, []byte("Backend:\n  AuthWebhookCacheDisabled: false\n"), 0600))

	conf, err := resolveServerConfig(server.NewConfig(), path, true)
	require.NoError(t, err)
	require.True(t, conf.Backend.AuthWebhookCacheDisabled)

	conf, err = resolveServerConfig(server.NewConfig(), path, false)
	require.NoError(t, err)
	require.False(t, conf.Backend.AuthWebhookCacheDisabled)

	conf, err = resolveServerConfig(server.NewConfig(), "", true)
	require.NoError(t, err)
	require.True(t, conf.Backend.AuthWebhookCacheDisabled)
}
