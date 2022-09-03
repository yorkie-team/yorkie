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
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/internal/version"
)

const commitRevisionKey = "vcs.revision"
const buildTimeKey = "vcs.time"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Yorkie",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Yorkie: %s\n", version.Version)
			fmt.Printf("Commit: %s\n", getBuildInfoByKey(commitRevisionKey))
			fmt.Printf("Go: %s\n", runtime.Version())
			fmt.Printf("Build date: %s\n", getBuildInfoByKey(buildTimeKey))
			return nil
		},
	}
}

func getBuildInfoByKey(key string) string {
	return func() string {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == key {
					return setting.Value
				}
			}
		}
		return ""
	}()
}

func init() {
	rootCmd.AddCommand(newVersionCmd())
}
