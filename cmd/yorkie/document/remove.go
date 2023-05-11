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

package document

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove [project name] [document key]",
		Short: "Remove documents in the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("project name & document key")
			}
			projectName := args[0]
			documentKey := args[1]

			token, err := config.LoadToken(config.AdminAddr)
			if err != nil {
				return err
			}
			cli, err := admin.Dial(config.AdminAddr, admin.WithToken(token))
			if err != nil {
				return err
			}
			defer func() {
				_ = cli.Close()
			}()

			ctx := context.Background()
			project, err := cli.GetProject(ctx, projectName)
			if err != nil {
				return err
			}
			apiKey := project.PublicKey
			success, err := cli.RemoveDocumentWithApiKey(ctx, projectName, documentKey, apiKey)
			if err != nil {
				return err
			}

			if !success {
				cmd.Printf("Fail remove document\n")
			}

			cmd.Printf("Success remove document\n")
			return nil
		},
	}
}

func init() {
	SubCmd.AddCommand(newRemoveCommand())
}
