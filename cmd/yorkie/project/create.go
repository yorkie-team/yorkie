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
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/admin"
)

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "create [name]",
		Short:   "Create a new project",
		Example: "yorkie project create sample-project",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(hackerwins): with random name.
			if len(args) != 1 {
				return errors.New("name is required")
			}

			name := args[0]

			// TODO(hackerwins): use adminAddr from env or addr flag.
			cli, err := admin.Dial("localhost:11103")
			if err != nil {
				return err
			}
			defer func() {
				_ = cli.Close()
			}()

			ctx := context.Background()
			project, err := cli.CreateProject(ctx, name)
			if err != nil {
				return err
			}

			encoded, err := json.Marshal(project)
			if err != nil {
				return err
			}

			fmt.Println(string(encoded))

			return nil
		},
	}
}

func init() {
	SubCmd.AddCommand(newCreateCommand())
}
