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
