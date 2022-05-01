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

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of Yorkie",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Yorkie: %s\n", version.Version)
			fmt.Printf("Commit: %s\n", version.GitCommit)
			fmt.Printf("Go: %s\n", runtime.Version())
			fmt.Printf("Build date: %s\n", version.BuildDate)
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(newVersionCmd())
}
