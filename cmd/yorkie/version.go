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
	"strings"

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
			if err := Validate(); err != nil {
				return err
			}

			var versionInfo types.VersionInfo
			versionInfo.ClientVersion = getYorkieClientVersion()

			serverVersionChan := make(chan *types.VersionDetail)
			errorChan := make(chan error)

			if !clientOnly {
				go func() {
					rpcAddr := viper.GetString("rpcAddr")
					auth, err := config.LoadAuth(rpcAddr)
					if err != nil {
						errorChan <- err
						return
					}

					cli, err := admin.Dial(rpcAddr, admin.WithToken(auth.Token), admin.WithInsecure(auth.Insecure))
					if err != nil {
						errorChan <- err
						return
					}
					defer cli.Close()

					ctx := context.Background()
					sv, err := cli.GetServerVersion(ctx)
					if err != nil {
						errorChan <- err
						return
					}

					serverVersionChan <- sv
				}()
			}

			var serverErr error

			if !clientOnly {
				select {
				case sv := <-serverVersionChan:
					versionInfo.ServerVersion = sv
				case err := <-errorChan:
					serverErr = err
				}
			}

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

			if serverErr != nil {
				cmd.Printf("Error fetching server version: ")
				if strings.Contains(serverErr.Error(), "unimplemented") {
					cmd.Printf("The server does not support this operation. You might need to check your server version.\n")
				} else {
					cmd.Print(serverErr)
				}
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
