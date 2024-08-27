/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	"context"
	"encoding/json"
	"errors"
	"runtime"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/internal/version"
)

var (
	clientOnly bool
	output     string
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the version number of Yorkie",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputOpts(); err != nil {
				return err
			}

			info := types.VersionInfo{
				ClientVersion: clientVersion(),
			}

			var serverErr error
			if !clientOnly {
				info.ServerVersion, serverErr = fetchServerVersion()
			}

			if err := printVersionInfo(cmd, output, &info); err != nil {
				return err
			}

			if serverErr != nil {
				printServerError(cmd, serverErr)
			}

			return nil
		},
	}
}

func fetchServerVersion() (*types.VersionDetail, error) {
	rpcAddr := viper.GetString("rpcAddr")
	auth, err := config.LoadAuth(rpcAddr)
	if err != nil {
		return nil, err
	}

	cli, err := admin.Dial(rpcAddr, admin.WithToken(auth.Token), admin.WithInsecure(auth.Insecure))
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	sv, err := cli.GetServerVersion(context.Background())
	if err != nil {
		return nil, err
	}

	return sv, nil
}

func clientVersion() *types.VersionDetail {
	return &types.VersionDetail{
		YorkieVersion: version.Version,
		GoVersion:     runtime.Version(),
		BuildDate:     version.BuildDate,
	}
}

func printServerError(cmd *cobra.Command, err error) {
	cmd.Print("Error fetching server version: ")

	// TODO(hyun98): Find cases where different error cases can occur,
	// and display a user-friendly error message for each case.
	// Furthermore, it would be good to improve it by creating a
	// general-purpose error handling module for rpc communication.
	// For more information, see the following link:
	// https://connectrpc.com/docs/go/errors/
	var connectErr *connect.Error
	if errors.As(err, &connectErr) && connectErr.Code() == connect.CodeUnimplemented {
		cmd.Println("The server does not support this operation. You might need to check your server version.")
		return
	}

	cmd.Println(err)
}

func printVersionInfo(cmd *cobra.Command, output string, versionInfo *types.VersionInfo) error {
	switch output {
	case "":
		cmd.Printf("Yorkie Client: %s\n", versionInfo.ClientVersion.YorkieVersion)
		cmd.Printf("Go: %s\n", versionInfo.ClientVersion.GoVersion)
		cmd.Printf("Build Date: %s\n", versionInfo.ClientVersion.BuildDate)
		if versionInfo.ServerVersion != nil {
			cmd.Printf("Yorkie Server: %s\n", versionInfo.ServerVersion.YorkieVersion)
			cmd.Printf("Go: %s\n", versionInfo.ServerVersion.GoVersion)
			cmd.Printf("Build Date: %s\n", versionInfo.ServerVersion.BuildDate)
		}
	case "yaml":
		marshalled, err := yaml.Marshal(versionInfo)
		if err != nil {
			return errors.New("failed to marshal YAML")
		}
		cmd.Println(string(marshalled))
	case "json":
		marshalled, err := json.MarshalIndent(versionInfo, "", "  ")
		if err != nil {
			return errors.New("failed to marshal JSON")
		}
		cmd.Println(string(marshalled))
	default:
		return errors.New("unknown output format")
	}

	return nil
}

// validateOutputOpts validates the output options.
func validateOutputOpts() error {
	if output != "" && output != "yaml" && output != "json" {
		return errors.New(`--output must be 'yaml' or 'json'`)
	}

	return nil
}

func init() {
	cmd := newVersionCmd()

	cmd.Flags().BoolVar(
		&clientOnly,
		"client",
		clientOnly,
		"Shows client version only (no server required).",
	)

	// TODO(hackerwins): Output format should be configurable globally.
	// So, we need to move this to the root command like `--rpc-addr` and
	// apply it to all subcommands that print output.
	cmd.Flags().StringVarP(
		&output,
		"output",
		"o",
		output,
		"One of 'yaml' or 'json'.",
	)

	rootCmd.AddCommand(cmd)
}
