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
	"github.com/spf13/viper"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	flagAuthWebhookURL            string
	flagName                      string
	flagClientDeactivateThreshold string
)

func newUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "update [name]",
		Short:   "Update a project",
		Example: "yorkie project update name [options]",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("name is required")
			}

			name := args[0]

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
			project, err := cli.GetProject(ctx, name)
			if err != nil {
				return err
			}
			id := project.ID.String()

			newName := name
			if flagName != "" {
				newName = flagName
			}

			newAuthWebhookURL := project.AuthWebhookURL
			if cmd.Flags().Lookup("auth-webhook-url").Changed { // allow empty string
				newAuthWebhookURL = flagAuthWebhookURL
			}

			newClientDeactivateThreshold := project.ClientDeactivateThreshold
			if flagClientDeactivateThreshold != "" {
				newClientDeactivateThreshold = flagClientDeactivateThreshold
			}

			updatableProjectFields := &types.UpdatableProjectFields{
				Name:                      &newName,
				AuthWebhookURL:            &newAuthWebhookURL,
				ClientDeactivateThreshold: &newClientDeactivateThreshold,
			}

			updated, err := cli.UpdateProject(ctx, id, updatableProjectFields)
			if err != nil {
				// TODO(chacha912): consider creating the error details type to remove the dependency on gRPC.
				st := status.Convert(err)
				for _, detail := range st.Details() {
					switch t := detail.(type) {
					case *errdetails.BadRequest:
						for _, violation := range t.GetFieldViolations() {
							cmd.Printf("Invalid Fields: The %q field was wrong: %s\n", violation.GetField(), violation.GetDescription())
						}
					}
				}

				return err
			}

			encoded, err := json.Marshal(updated)
			if err != nil {
				return fmt.Errorf("marshal project: %w", err)
			}

			cmd.Println(string(encoded))

			return nil
		},
	}
}

func init() {
	cmd := newUpdateCommand()
	cmd.Flags().StringVar(
		&flagName,
		"name",
		"",
		"new project name",
	)
	cmd.Flags().StringVar(
		&flagAuthWebhookURL,
		"auth-webhook-url",
		"",
		"authorization-webhook update url",
	)
	cmd.Flags().StringVar(
		&flagClientDeactivateThreshold,
		"client-deactivate-threshold",
		"",
		"client deactivate threshold for housekeeping",
	)
	SubCmd.AddCommand(cmd)
}
