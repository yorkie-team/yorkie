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
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/internal/version"
	"gopkg.in/yaml.v3"
)

var (
	clientOnly bool
	output     string
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Yorkie",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := Validate(); err != nil {
				return err
			}

			var versionInfo types.VersionInfo
			versionInfo.ClientVersion = getYorkieClientVersion()

			if !clientOnly {
				rpcAddr := viper.GetString("rpcAddr")
				auth, err := config.LoadAuth(rpcAddr)
				if err != nil {
					return err
				}

				cli, err := admin.Dial(rpcAddr, admin.WithToken(auth.Token), admin.WithInsecure(auth.Insecure))
				if err != nil {
					return err
				}
				defer func() {
					cli.Close()
				}()

				ctx := context.Background()
				sv, err := cli.GetServerVersion(ctx)
				if err != nil {
					return err
				}

				versionInfo.ServerVersion = sv
			}

			switch output {
			case "":
				fmt.Printf("Yorkie Client: %s\n", versionInfo.ClientVersion.YorkieVersion)
				fmt.Printf("Go: %s\n", versionInfo.ClientVersion.GoVersion)
				fmt.Printf("Build Date: %s\n", versionInfo.ClientVersion.BuildDate)
				if versionInfo.ServerVersion != nil {
					fmt.Printf("Yorkie Server: %s\n", versionInfo.ServerVersion.YorkieVersion)
					fmt.Printf("Go: %s\n", versionInfo.ServerVersion.GoVersion)
					fmt.Printf("Build Date: %s\n", versionInfo.ServerVersion.BuildDate)
				}
			case "yaml":
				marshalled, err := yaml.Marshal(&versionInfo)
				if err != nil {
					return errors.New("failed to marshal YAML")
				}
				fmt.Println(string(marshalled))
			case "json":
				marshalled, err := json.MarshalIndent(&versionInfo, "", "  ")
				if err != nil {
					return errors.New("failed to marshal JSON")
				}
				fmt.Println(string(marshalled))
			}

			return nil
		},
	}
}

// Validate validates the provided options.
func Validate() error {
	if output != "" && output != "yaml" && output != "json" {
		return errors.New(`--output must be 'yaml' or 'json'`)
	}

	return nil
}

func getYorkieClientVersion() *types.VersionDetail {
	return &types.VersionDetail{
		YorkieVersion: version.Version,
		GoVersion:     runtime.Version(),
		BuildDate:     version.BuildDate,
	}
}

func init() {
	cmd := newVersionCmd()
	cmd.Flags().BoolVar(
		&clientOnly,
		"client",
		clientOnly,
		"Shows client version only. (no server required)",
	)
	cmd.Flags().StringVarP(
		&output,
		"output",
		"o",
		output,
		"One of 'yaml' or 'json'.",
	)

	rootCmd.AddCommand(cmd)
}
