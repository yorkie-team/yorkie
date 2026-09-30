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

	t.Run("explicit flags survive the config file", func(t *testing.T) {
		base, flags := serverConfigFlags(t,
			"--backend-secret-key=flag-secret",
			"--backend-admin-user=flag-user",
			"--backend-admin-password=flag-password",
		)

		conf, err := resolveServerConfig(base, path, flags, false)
		require.NoError(t, err)
		require.Equal(t, "flag-secret", conf.Backend.SecretKey)
		require.Equal(t, "flag-user", conf.Backend.AdminUser)
		require.Equal(t, "flag-password", conf.Backend.AdminPassword)
	})

	t.Run("untouched flags leave the file value alone", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "server.yml")
		require.NoError(t, os.WriteFile(filePath, []byte("Backend:\n  SecretKey: file-secret\n"), 0600))

		base, flags := serverConfigFlags(t)
		conf, err := resolveServerConfig(base, filePath, flags, false)
		require.NoError(t, err)
		require.Equal(t, "file-secret", conf.Backend.SecretKey)
	})
}

// serverConfigFlags parses the registered server flags without starting a server.
// Restore their bound values so each case has an independent configuration.
func serverConfigFlags(t *testing.T, args ...string) (*server.Config, *pflag.FlagSet) {
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
	return &base, flags
}

func TestResolveServerConfigRejectsExplicitEmptyCredentials(t *testing.T) {
	for _, name := range []string{"backend-secret-key", "backend-admin-user", "backend-admin-password"} {
		for _, withFile := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/config=%t", name, withFile), func(t *testing.T) {
				path := ""
				if withFile {
					path = filepath.Join(t.TempDir(), "server.yml")
					require.NoError(t, os.WriteFile(path, []byte("Backend:\n  SecretKey: test-file-key\n"+
						"  AdminUser: test-file-user\n  AdminPassword: test-file-password\n"), 0600))
				}
				base, flags := serverConfigFlags(t, "--"+name+"=")
				resolved, err := resolveServerConfig(base, path, flags, false)
				require.EqualError(t, err, "--"+name+" must not be empty")
				require.Nil(t, resolved)
			})
		}
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
			base, flags := serverConfigFlags(t)
			resolved, err := resolveServerConfig(base, path, flags, false)
			require.NoError(t, err)
			require.True(t, resolved.Backend.SecretKey == server.DefaultSecretKey)
			require.True(t, resolved.Backend.AdminUser == server.DefaultAdminUser)
			require.True(t, resolved.Backend.AdminPassword == server.DefaultAdminPassword)
			require.Empty(t, resolved.Backend.ClusterSecret)
		})
		t.Run(fmt.Sprintf("empty cluster secret/config=%t", withFile), func(t *testing.T) {
			path := ""
			if withFile {
				path = filepath.Join(t.TempDir(), "server.yml")
				require.NoError(t, os.WriteFile(path, []byte("Backend:\n  ClusterSecret: test-cluster-secret\n"), 0600))
			}
			base, flags := serverConfigFlags(t, "--cluster-secret=")
			resolved, err := resolveServerConfig(base, path, flags, false)
			require.NoError(t, err)
			require.Empty(t, resolved.Backend.ClusterSecret)
		})
	}
}

func TestResolveServerConfigKeepsExplicitWebhookSecurityFlags(t *testing.T) {
	for _, tc := range []struct {
		name          string
		file          string
		args          []string
		validation    bool
		ttl           time.Duration
		cacheDisabled bool
	}{
		{"validation true", "false", []string{"--backend-enable-webhook-validation=true"}, true, time.Hour, false},
		{"validation false", "true", []string{"--backend-enable-webhook-validation=false"}, false, time.Hour, false},
		{"explicit TTL", "true", []string{"--auth-webhook-cache-auth-ttl=1ms"}, true, time.Millisecond, false},
		{"omitted flags", "true", nil, true, time.Hour, false},
		{"cache disabled", "true", []string{"--auth-webhook-cache-disabled", "--auth-webhook-cache-auth-ttl=1ms"},
			true, time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.yml")
			require.NoError(t, os.WriteFile(path, []byte("RPC:\n  Port: 18123\nBackend:\n"+
				"  EnableWebhookValidation: "+tc.file+"\n  AuthWebhookCacheTTL: 1h\n"+
				"  AuthWebhookCacheDisabled: false\n"), 0600))
			base, flags := serverConfigFlags(t, append(tc.args, "--rpc-port=18124")...)
			resolved, err := resolveServerConfig(base, path, flags, authWebhookCacheDisabled)
			require.NoError(t, err)
			require.Equal(t, tc.validation, resolved.Backend.EnableWebhookValidation)
			require.Equal(t, tc.ttl, resolved.Backend.ParseAuthWebhookCacheTTL())
			require.Equal(t, tc.cacheDisabled, resolved.Backend.AuthWebhookCacheDisabled)
			require.Equal(t, 18123, resolved.RPC.Port, "ordinary flags retain file precedence")
			require.NoError(t, resolved.Validate())
		})
	}
}

func TestResolveServerConfigDoesNotDiscardInvalidExplicitWebhookTTL(t *testing.T) {
	for _, ttl := range []string{"0s", "-1s", "1ns"} {
		for _, disabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/disabled=%t", ttl, disabled), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "server.yml")
				require.NoError(t, os.WriteFile(path, []byte("Backend:\n  AuthWebhookCacheTTL: 1h\n"), 0600))
				base, flags := serverConfigFlags(t, "--auth-webhook-cache-auth-ttl="+ttl,
					fmt.Sprintf("--auth-webhook-cache-disabled=%t", disabled))
				resolved, err := resolveServerConfig(base, path, flags, authWebhookCacheDisabled)
				require.NoError(t, err)
				err = resolved.Validate()
				require.ErrorContains(t, err, "--auth-webhook-cache-auth-ttl")
				require.ErrorContains(t, err, "at least 1ms")
			})
		}
	}
}

func TestResolveServerConfigKeepsFileCacheDisableWithFalseFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	require.NoError(t, os.WriteFile(path, []byte("Backend:\n  AuthWebhookCacheDisabled: true\n"), 0600))
	base, flags := serverConfigFlags(t, "--auth-webhook-cache-disabled=false")
	resolved, err := resolveServerConfig(base, path, flags, authWebhookCacheDisabled)
	require.NoError(t, err)
	require.True(t, resolved.Backend.AuthWebhookCacheDisabled)
}

func TestResolveServerConfigKeepsWebhookFlagsWithoutFile(t *testing.T) {
	for _, validation := range []bool{false, true} {
		t.Run(fmt.Sprintf("validation=%t", validation), func(t *testing.T) {
			base, flags := serverConfigFlags(t,
				fmt.Sprintf("--backend-enable-webhook-validation=%t", validation),
				"--auth-webhook-cache-auth-ttl=1ms", "--auth-webhook-cache-disabled")
			resolved, err := resolveServerConfig(base, "", flags, authWebhookCacheDisabled)
			require.NoError(t, err)
			require.Equal(t, validation, resolved.Backend.EnableWebhookValidation)
			require.Equal(t, time.Millisecond, resolved.Backend.ParseAuthWebhookCacheTTL())
			require.True(t, resolved.Backend.AuthWebhookCacheDisabled)
			require.NoError(t, resolved.Validate())
		})
	}
}
