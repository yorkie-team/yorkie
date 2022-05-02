/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package project

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/admin"
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(hackerwins): use adminAddr from env or addr flag.
			cli, err := admin.Dial("localhost:11103")
			if err != nil {
				return err
			}
			defer func() {
				_ = cli.Close()
			}()

			ctx := context.Background()
			projects, err := cli.ListProjects(ctx)
			if err != nil {
				return err
			}

			// TODO(hackerwins): Print projects in table format.
			for _, project := range projects {
				cmd.Printf("%s\n", project.Name)
			}

			return nil
		},
	}
}

func init() {
	SubCmd.AddCommand(newListCommand())
}
