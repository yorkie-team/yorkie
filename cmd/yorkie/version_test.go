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
	"bytes"
	"encoding/json"
	"testing"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
)

const serverErrPrefix = "Error fetching server version: "

// runVersionCmd runs the version command with the given --output format
// against an address that has no stored auth, so fetching the server version
// always fails. It returns what the command wrote to stdout and to stderr.
func runVersionCmd(t *testing.T, output string) (string, string) {
	t.Helper()

	// Point HOME at a temp dir so the real CLI config is neither read nor
	// written, which makes the missing auth entry deterministic.
	t.Setenv("HOME", t.TempDir())

	prevOutput := viper.GetString("output")
	prevRPCAddr := viper.GetString("rpcAddr")
	t.Cleanup(func() {
		viper.Set("output", prevOutput)
		viper.Set("rpcAddr", prevRPCAddr)
	})
	viper.Set("output", output)
	viper.Set("rpcAddr", "localhost:1")

	cmd := newVersionCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	require.NoError(t, cmd.RunE(cmd, nil))

	return stdout.String(), stderr.String()
}

// TestVersionCmdStreamsServerErrorToStderr pins the contract that --output
// json and --output yaml leave stdout machine-readable: the server-fetch
// error belongs on stderr, not interleaved with the version document.
func TestVersionCmdStreamsServerErrorToStderr(t *testing.T) {
	t.Run("json output keeps stdout parsable", func(t *testing.T) {
		stdout, stderr := runVersionCmd(t, JSONOutput)

		var info types.VersionInfo
		require.NoError(t, json.Unmarshal([]byte(stdout), &info))
		require.NotNil(t, info.ClientVersion)
		assert.Equal(t, version.Version, info.ClientVersion.YorkieVersion)
		assert.Nil(t, info.ServerVersion)

		assert.NotContains(t, stdout, serverErrPrefix)
		assert.Contains(t, stderr, serverErrPrefix)
	})

	t.Run("yaml output keeps stdout parsable", func(t *testing.T) {
		stdout, stderr := runVersionCmd(t, YamlOutput)

		var info types.VersionInfo
		require.NoError(t, yaml.Unmarshal([]byte(stdout), &info))
		require.NotNil(t, info.ClientVersion)
		assert.Equal(t, version.Version, info.ClientVersion.YorkieVersion)
		assert.Nil(t, info.ServerVersion)

		assert.NotContains(t, stdout, serverErrPrefix)
		assert.Contains(t, stderr, serverErrPrefix)
	})

	t.Run("table output keeps the client version on stdout", func(t *testing.T) {
		stdout, stderr := runVersionCmd(t, DefaultOutput)

		assert.Contains(t, stdout, "Yorkie Client: "+version.Version)
		assert.NotContains(t, stdout, "Yorkie Server: ")
		assert.NotContains(t, stdout, serverErrPrefix)
		assert.Contains(t, stderr, serverErrPrefix)
	})
}

// TestPrintServerError covers both message branches, since each one writes
// through a different call.
func TestPrintServerError(t *testing.T) {
	printTo := func(err error) (string, string) {
		cmd := &cobra.Command{}
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)

		printServerError(cmd, err)

		return stdout.String(), stderr.String()
	}

	t.Run("generic error", func(t *testing.T) {
		stdout, stderr := printTo(connect.NewError(connect.CodeUnavailable, assert.AnError))

		assert.Empty(t, stdout)
		assert.Contains(t, stderr, serverErrPrefix)
		assert.Contains(t, stderr, assert.AnError.Error())
	})

	t.Run("unimplemented error", func(t *testing.T) {
		stdout, stderr := printTo(connect.NewError(connect.CodeUnimplemented, assert.AnError))

		assert.Empty(t, stdout)
		assert.Contains(t, stderr, serverErrPrefix)
		assert.Contains(t, stderr, "The server does not support this operation.")
	})
}
