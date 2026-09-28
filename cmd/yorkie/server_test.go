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

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server"
)

func TestResolveServerConfigKeepsExplicitCacheDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	require.NoError(t, os.WriteFile(path, []byte("Backend:\n  AuthWebhookCacheDisabled: false\n"), 0600))

	conf, err := resolveServerConfig(server.NewConfig(), path, nil, true)
	require.NoError(t, err)
	require.True(t, conf.Backend.AuthWebhookCacheDisabled)

	conf, err = resolveServerConfig(server.NewConfig(), path, nil, false)
	require.NoError(t, err)
	require.False(t, conf.Backend.AuthWebhookCacheDisabled)

	conf, err = resolveServerConfig(server.NewConfig(), "", nil, true)
	require.NoError(t, err)
	require.True(t, conf.Backend.AuthWebhookCacheDisabled)
}

func TestResolveServerConfigKeepsExplicitCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	require.NoError(t, os.WriteFile(path, []byte("Backend:\n  UseDefaultProject: true\n"), 0600))

	newFlags := func() *pflag.FlagSet {
		flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
		flags.String("backend-secret-key", server.DefaultSecretKey, "")
		flags.String("backend-admin-user", server.DefaultAdminUser, "")
		flags.String("backend-admin-password", server.DefaultAdminPassword, "")
		return flags
	}

	t.Run("explicit flags survive the config file", func(t *testing.T) {
		base := server.NewConfig()
		base.Backend.SecretKey = "flag-secret"
		base.Backend.AdminUser = "flag-user"
		base.Backend.AdminPassword = "flag-password"

		flags := newFlags()
		require.NoError(t, flags.Parse([]string{
			"--backend-secret-key=flag-secret",
			"--backend-admin-user=flag-user",
			"--backend-admin-password=flag-password",
		}))

		conf, err := resolveServerConfig(base, path, flags, false)
		require.NoError(t, err)
		require.Equal(t, "flag-secret", conf.Backend.SecretKey)
		require.Equal(t, "flag-user", conf.Backend.AdminUser)
		require.Equal(t, "flag-password", conf.Backend.AdminPassword)
	})

	t.Run("untouched flags leave the file value alone", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "server.yml")
		require.NoError(t, os.WriteFile(filePath, []byte("Backend:\n  SecretKey: file-secret\n"), 0600))

		conf, err := resolveServerConfig(server.NewConfig(), filePath, newFlags(), false)
		require.NoError(t, err)
		require.Equal(t, "file-secret", conf.Backend.SecretKey)
	})
}
