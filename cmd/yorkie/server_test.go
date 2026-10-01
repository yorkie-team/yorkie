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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// serverConfigFlags parses the registered server flags without starting a server.
// Restore their bound values so each case has an independent configuration.
func serverConfigFlags(t *testing.T, args ...string) *server.Config {
	t.Helper()
	cmd, _, err := rootCmd.Find([]string{"server"})
	require.NoError(t, err)
	flags := cmd.Flags()
	for _, name := range []string{
		"backend-secret-key", "backend-admin-user", "backend-admin-password", "cluster-secret",
		"backend-enable-webhook-validation", "auth-webhook-cache-auth-ttl",
		"auth-webhook-cache-disabled", "rpc-port",
	} {
		flag := flags.Lookup(name)
		require.NotNil(t, flag, "server flag must be registered: %s", name)
		value, changed := flag.Value.String(), flag.Changed
		t.Cleanup(func() {
			require.NoError(t, flag.Value.Set(value))
			flag.Changed = changed
		})
	}
	require.NoError(t, flags.Parse(args))
	base := *conf
	backend := *conf.Backend
	base.Backend = &backend
	base.Backend.AuthWebhookCacheTTL = authWebhookCacheTTL.String()
	return &base
}

// A config file keeps its long-standing precedence over every flag, so no
// command line can quietly replace a secret, a cluster secret or an SSRF
// guard that the operator wrote into the file.
func TestResolveServerConfigKeepsFileValuesOverFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	require.NoError(t, os.WriteFile(path, []byte("RPC:\n  Port: 18123\nBackend:\n"+
		"  SecretKey: file-secret\n  AdminUser: file-user\n  AdminPassword: file-password\n"+
		"  ClusterSecret: file-cluster-secret\n  EnableWebhookValidation: true\n"+
		"  AuthWebhookCacheTTL: 1h\n"), 0600))

	base := serverConfigFlags(t,
		"--backend-secret-key=flag-secret",
		"--backend-admin-user=flag-user",
		"--backend-admin-password=flag-password",
		"--cluster-secret=",
		"--backend-enable-webhook-validation=false",
		"--auth-webhook-cache-auth-ttl=1ms",
		"--rpc-port=18124",
	)
	resolved, err := resolveServerConfig(base, path, false)
	require.NoError(t, err)
	require.Equal(t, "file-secret", resolved.Backend.SecretKey)
	require.Equal(t, "file-user", resolved.Backend.AdminUser)
	require.Equal(t, "file-password", resolved.Backend.AdminPassword)
	require.Equal(t, "file-cluster-secret", resolved.Backend.ClusterSecret)
	require.True(t, resolved.Backend.EnableWebhookValidation)
	require.Equal(t, time.Hour, resolved.Backend.ParseAuthWebhookCacheTTL())
	require.Equal(t, 18123, resolved.RPC.Port)
	require.NoError(t, resolved.Validate())
}

// Without a config file the flags are the whole configuration, and an
// explicitly emptied credential has to be refused by validation rather than
// silently signing admin tokens with the empty key.
func TestServerConfigRejectsExplicitEmptyCredentials(t *testing.T) {
	for _, name := range []string{"backend-secret-key", "backend-admin-user", "backend-admin-password"} {
		t.Run(name, func(t *testing.T) {
			base := serverConfigFlags(t, "--"+name+"=")
			resolved, err := resolveServerConfig(base, "", false)
			require.NoError(t, err)
			require.ErrorContains(t, resolved.Validate(), "--"+name)
			require.ErrorContains(t, resolved.Validate(), "must not be empty")
		})
	}
}

func TestResolveServerConfigPreservesOptionalClusterSecretAndDefaults(t *testing.T) {
	for _, withFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("development defaults/config=%t", withFile), func(t *testing.T) {
			path := ""
			if withFile {
				path = filepath.Join(t.TempDir(), "server.yml")
				require.NoError(t, os.WriteFile(path, []byte("Backend:\n  UseDefaultProject: true\n"), 0600))
			}
			resolved, err := resolveServerConfig(serverConfigFlags(t), path, false)
			require.NoError(t, err)
			require.Equal(t, server.DefaultSecretKey, resolved.Backend.SecretKey)
			require.Equal(t, server.DefaultAdminUser, resolved.Backend.AdminUser)
			require.Equal(t, server.DefaultAdminPassword, resolved.Backend.AdminPassword)
			require.Empty(t, resolved.Backend.ClusterSecret)
			require.NoError(t, resolved.Validate())
		})
	}
}

func TestResolveServerConfigKeepsFileCacheDisableWithFalseFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	require.NoError(t, os.WriteFile(path, []byte("Backend:\n  AuthWebhookCacheDisabled: true\n"), 0600))
	base := serverConfigFlags(t, "--auth-webhook-cache-disabled=false")
	resolved, err := resolveServerConfig(base, path, authWebhookCacheDisabled)
	require.NoError(t, err)
	require.True(t, resolved.Backend.AuthWebhookCacheDisabled)
}

func TestResolveServerConfigKeepsWebhookFlagsWithoutFile(t *testing.T) {
	for _, validation := range []bool{false, true} {
		t.Run(fmt.Sprintf("validation=%t", validation), func(t *testing.T) {
			base := serverConfigFlags(t,
				fmt.Sprintf("--backend-enable-webhook-validation=%t", validation),
				"--auth-webhook-cache-auth-ttl=1ms", "--auth-webhook-cache-disabled")
			resolved, err := resolveServerConfig(base, "", authWebhookCacheDisabled)
			require.NoError(t, err)
			require.Equal(t, validation, resolved.Backend.EnableWebhookValidation)
			require.Equal(t, time.Millisecond, resolved.Backend.ParseAuthWebhookCacheTTL())
			require.True(t, resolved.Backend.AuthWebhookCacheDisabled)
			require.NoError(t, resolved.Validate())
		})
	}
}

// An invalid TTL supplied on the command line must fail validation instead of
// being accepted; the file-precedence rule only applies when a file is given.
func TestServerConfigRejectsInvalidWebhookTTLWithoutFile(t *testing.T) {
	for _, ttl := range []string{"0s", "-1s", "1ns"} {
		t.Run(ttl, func(t *testing.T) {
			base := serverConfigFlags(t, "--auth-webhook-cache-auth-ttl="+ttl)
			resolved, err := resolveServerConfig(base, "", authWebhookCacheDisabled)
			require.NoError(t, err)
			err = resolved.Validate()
			require.ErrorContains(t, err, "--auth-webhook-cache-auth-ttl")
			require.ErrorContains(t, err, "at least 1ms")
		})
	}
}
