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

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "create [name]",
		Short:   "Create a new project",
		Example: "yorkie project create sample-project",
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
			project, err := cli.CreateProject(ctx, name)
			if err != nil {
				var connErr *connect.Error
				if errors.As(err, &connErr) {
					for _, detail := range connErr.Details() {
						value, err := detail.Value()
						if err != nil {
							continue
						}

						badReq, ok := value.(*errdetails.BadRequest)
						if !ok {
							continue
						}

						for _, violation := range badReq.GetFieldViolations() {
							cmd.Printf("Invalid Field: %q - %s\n", violation.GetField(), violation.GetDescription())
						}
					}
				}
				return err
			}

			output := viper.GetString("output")
			if err := printCreateProjectInfo(cmd, output, project); err != nil {
				return err
			}

			return nil
		},
	}
}

func printCreateProjectInfo(cmd *cobra.Command, output string, project *types.Project) error {
	switch output {
	case JSONOutput, DefaultOutput:
		encoded, err := json.Marshal(project)
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		cmd.Println(string(encoded))
	case YamlOutput:
		marshalled, err := yaml.Marshal(project)
		if err != nil {
			return fmt.Errorf("marshal YAML: %w", err)
		}
		cmd.Println(string(marshalled))
	default:
		return fmt.Errorf("unknown output format: %s", output)
	}

	return nil
}

func init() {
	SubCmd.AddCommand(newCreateCommand())
}
