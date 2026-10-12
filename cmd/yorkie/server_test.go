/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server"
)

func serverCmd(t *testing.T) *cobra.Command {
	t.Helper()

	for _, c := range rootCmd.Commands() {
		if c.Name() == "server" {
			return c
		}
	}

	t.Fatal("server command not registered")
	return nil
}

// TestServerCmdFlagDefaults pins the server flag defaults to their canonical
// constants, so a flag default can never drift from the value used elsewhere
// (e.g. ensureMongoDefaultValue) the way --mongo-project-cache-ttl once did.
func TestServerCmdFlagDefaults(t *testing.T) {
	cmd := serverCmd(t)

	t.Run("mongo-project-cache-ttl defaults to DefaultProjectCacheTTL", func(t *testing.T) {
		f := cmd.Flags().Lookup("mongo-project-cache-ttl")
		assert.NotNil(t, f)
		assert.Equal(t, server.DefaultProjectCacheTTL.String(), f.DefValue)
	})

	t.Run("mongo-project-cache-size defaults to DefaultProjectCacheSize", func(t *testing.T) {
		f := cmd.Flags().Lookup("mongo-project-cache-size")
		assert.NotNil(t, f)
		assert.Equal(t, strconv.Itoa(server.DefaultProjectCacheSize), f.DefValue)
	})

	// An unset key is generated at startup; a public default would let
	// anyone forge an admin token (#2103).
	t.Run("backend-secret-key has no default", func(t *testing.T) {
		f := cmd.Flags().Lookup("backend-secret-key")
		assert.NotNil(t, f)
		assert.Empty(t, f.DefValue)
	})
}

// TestServerCmdBlankSecretKey checks that a --backend-secret-key given on the
// command line but blank stops the server before it starts. Unset, it falls
// back to the environment or a random key; given blank, the operator meant to
// share a key, and a per-process one would make replicas reject each other's
// tokens.
func TestServerCmdBlankSecretKey(t *testing.T) {
	cmd := serverCmd(t)
	t.Cleanup(func() {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				assert.NoError(t, f.Value.Set(f.DefValue))
				f.Changed = false
			}
		})
	})

	// The --config path does not exist, so a run that gets past the key
	// check fails on reading it instead of starting a server.
	missing := filepath.Join(t.TempDir(), "missing.yml")
	for _, value := range []string{"", "  "} {
		assert.NoError(t, cmd.ParseFlags([]string{
			"--config", missing,
			"--backend-secret-key=" + value,
		}))
		err := cmd.RunE(cmd, nil)
		assert.ErrorContains(t, err, "--backend-secret-key")
	}
}

// TestServerCmdCredentialEnv checks that the credentials a deployment should
// keep off the command line — the MongoDB connection URI, which carries the
// database password, the cluster RPC secret and the GitHub OAuth client
// secret — are read from the environment when the flags leave them unset. An
// argument is readable in /proc/<pid>/cmdline and through the pprof cmdline
// endpoint, which the Helm chart enables and serves on the profiling port.
func TestServerCmdCredentialEnv(t *testing.T) {
	cmd := serverCmd(t)
	uri, secret := mongoConnectionURI, conf.Backend.ClusterSecret
	clientSecret := authGitHubClientSecret
	t.Cleanup(func() {
		mongoConnectionURI, conf.Backend.ClusterSecret = uri, secret
		authGitHubClientSecret = clientSecret
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				assert.NoError(t, f.Value.Set(f.DefValue))
				f.Changed = false
			}
		})
	})

	// Surrounding whitespace is dropped, so a value read from a file-mounted
	// Secret with a trailing newline still matches the one other nodes use.
	t.Setenv(MongoConnectionURIEnv, " mongodb://user:pw@mongo:27017 \n")
	t.Setenv(ClusterSecretEnv, "shared-cluster-secret\n")
	t.Setenv(GitHubClientSecretEnv, " github-client-secret\n")

	// The --config path does not exist, so the run stops right after the
	// environment is read, rather than starting a server.
	missing := filepath.Join(t.TempDir(), "missing.yml")
	assert.NoError(t, cmd.ParseFlags([]string{"--config", missing}))
	assert.Error(t, cmd.RunE(cmd, nil))

	assert.Equal(t, "mongodb://user:pw@mongo:27017", mongoConnectionURI)
	assert.Equal(t, "shared-cluster-secret", conf.Backend.ClusterSecret)
	assert.Equal(t, "github-client-secret", authGitHubClientSecret)
}

// TestServerCmdCredentialFlagsWin checks an explicit flag is not replaced by
// the environment: a deployment that still passes the credential on the
// command line keeps working unchanged.
func TestServerCmdCredentialFlagsWin(t *testing.T) {
	cmd := serverCmd(t)
	uri, secret := mongoConnectionURI, conf.Backend.ClusterSecret
	clientSecret := authGitHubClientSecret
	t.Cleanup(func() {
		mongoConnectionURI, conf.Backend.ClusterSecret = uri, secret
		authGitHubClientSecret = clientSecret
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				assert.NoError(t, f.Value.Set(f.DefValue))
				f.Changed = false
			}
		})
	})

	t.Setenv(MongoConnectionURIEnv, "mongodb://from-env:27017")
	t.Setenv(ClusterSecretEnv, "from-env")
	t.Setenv(GitHubClientSecretEnv, "from-env")

	missing := filepath.Join(t.TempDir(), "missing.yml")
	assert.NoError(t, cmd.ParseFlags([]string{
		"--config", missing,
		"--mongo-connection-uri", "mongodb://from-flag:27017",
		"--cluster-secret", "from-flag",
		"--auth-github-client-secret", "from-flag",
	}))
	assert.Error(t, cmd.RunE(cmd, nil))

	assert.Equal(t, "mongodb://from-flag:27017", mongoConnectionURI)
	assert.Equal(t, "from-flag", conf.Backend.ClusterSecret)
	assert.Equal(t, "from-flag", authGitHubClientSecret)
}
