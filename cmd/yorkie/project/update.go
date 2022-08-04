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
	"github.com/yorkie-team/yorkie/api/types"
)

var (
	flagAuthWebHookUrl string
	flagName           string

	ErrProjectNotFound = errors.New("not found project")
)

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "update [name]",
		Short:   "Update a project",
		Example: "yorkie project update name [flags]",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("project name is required")
			}

			name := args[0]

			newAuthWebhookURL := flagAuthWebHookUrl
			newAuthWebhookMethods := []string{
				string(types.AttachDocument),
				string(types.WatchDocuments),
			}

			updatableProjectFields := &types.UpdatableProjectFields{
				Name:               &name,
				AuthWebhookURL:     &newAuthWebhookURL,
				AuthWebhookMethods: &newAuthWebhookMethods,
			}

			// TODO(hackerwins): use adminAddr from env or addr flag.
			cli, err := admin.Dial("localhost:11103")
			if err != nil {
				return err
			}
			defer func() {
				_ = cli.Close()
			}()

			ctx := context.Background()
			exists, err := cli.GetProject(ctx, name)
			if err != nil {
				return err
			}
			if exists == nil {
				return ErrProjectNotFound
			}
			id := exists.ID.String()

			if flagName != "" {
				name = flagName
			}

			if flagAuthWebHookUrl == "" {
				newAuthWebhookURL = exists.AuthWebhookURL
			}

			project, err := cli.UpdateProject(ctx, id, updatableProjectFields)
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
	cmd := newUpdateCommand()
	cmd.Flags().StringVar(
		&flagAuthWebHookUrl,
		"auth-webhook-url",
		"",
		"authorization-webhook update url",
	)
	cmd.Flags().StringVar(
		&flagName,
		"name",
		"",
		"new project name",
	)
	SubCmd.AddCommand(cmd)
}
