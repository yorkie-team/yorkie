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
	"github.com/yorkie-team/yorkie/api/types"
	"gopkg.in/yaml.v3"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/pkg/units"
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List all projects",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			rpcAddr := viper.GetString("rpcAddr")

			output := viper.GetString("output")
			if err := validateOutputOpts(output); err != nil {
				return err
			}

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
			projects, err := cli.ListProjects(ctx)
			if err != nil {
				return err
			}

			outputFormat := viper.GetString("output")

			err2 := printProjects(cmd, outputFormat, projects)
			if err2 != nil {
				return err2
			}

			return nil
		},
	}
}

func printProjects(cmd *cobra.Command, outputFormat string, projects []*types.Project) error {
	switch outputFormat {
	case "json":
		jsonOutput, err := json.MarshalIndent(projects, "", "  ")
		if err != nil {
			return errors.New("error marshaling to JSON: %w")
		}
		cmd.Println(string(jsonOutput))
	case "yaml":
		yamlOutput, err := yaml.Marshal(projects)
		if err != nil {
			return errors.New("error marshaling to YAML: %w")
		}
		cmd.Println(string(yamlOutput))
	case "":
		tw := table.NewWriter()
		tw.Style().Options.DrawBorder = false
		tw.Style().Options.SeparateColumns = false
		tw.Style().Options.SeparateFooter = false
		tw.Style().Options.SeparateHeader = false
		tw.Style().Options.SeparateRows = false
		tw.AppendHeader(table.Row{
			"NAME",
			"PUBLIC KEY",
			"SECRET KEY",
			"AUTH WEBHOOK URL",
			"AUTH WEBHOOK METHODS",
			"CLIENT DEACTIVATE THRESHOLD",
			"CREATED AT",
		})
		for _, project := range projects {
			tw.AppendRow(table.Row{
				project.Name,
				project.PublicKey,
				project.SecretKey,
				project.AuthWebhookURL,
				project.AuthWebhookMethods,
				project.ClientDeactivateThreshold,
				units.HumanDuration(time.Now().UTC().Sub(project.CreatedAt)),
			})
		}
		cmd.Println(tw.Render())
	default:
		return errors.New("unknown output format")
	}

	return nil
}

func init() {
	SubCmd.AddCommand(newListCommand())
}
