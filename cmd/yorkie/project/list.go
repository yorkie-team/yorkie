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
	"fmt"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/pkg/units"
)

func newListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Short:   "List all projects",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			projects, err := cli.ListProjects(ctx)
			if err != nil {
				return err
			}

			output := viper.GetString("output")
			verbose, _ := cmd.Flags().GetBool("verbose")
			err2 := printProjects(cmd, output, projects, verbose)
			if err2 != nil {
				return err2
			}

			return nil
		},
	}

	cmd.Flags().BoolP("verbose", "v", false, "Show detailed project information")

	return cmd
}

func printProjects(cmd *cobra.Command, output string, projects []*types.Project, verbose bool) error {
	switch output {
	case DefaultOutput:
		tw := table.NewWriter()
		tw.Style().Options.DrawBorder = false
		tw.Style().Options.SeparateColumns = false
		tw.Style().Options.SeparateFooter = false
		tw.Style().Options.SeparateHeader = false
		tw.Style().Options.SeparateRows = false

		if verbose {
			// Show all fields when verbose flag is used
			tw.AppendHeader(table.Row{
				"NAME",
				"PUBLIC KEY",
				"SECRET KEY",
				"AUTH WEBHOOK URL",
				"AUTH WEBHOOK METHODS",
				"AUTH WEBHOOK MAX RETRIES",
				"AUTH WEBHOOK MIN WAIT INTERVAL",
				"AUTH WEBHOOK MAX WAIT INTERVAL",
				"AUTH WEBHOOK REQUEST TIMEOUT",
				"EVENT WEBHOOK URL",
				"EVENT WEBHOOK EVENTS",
				"EVENT WEBHOOK MAX RETRIES",
				"EVENT WEBHOOK MIN WAIT INTERVAL",
				"EVENT WEBHOOK MAX WAIT INTERVAL",
				"EVENT WEBHOOK REQUEST TIMEOUT",
				"CLIENT DEACTIVATE THRESHOLD",
				"SNAPSHOT THRESHOLD",
				"SNAPSHOT INTERVAL",
				"MAX SUBSCRIBERS PER DOCUMENT",
				"MAX ATTACHMENTS PER DOCUMENT",
				"REMOVE ON DETACH",
				"CREATED AT",
			})
			for _, project := range projects {
				tw.AppendRow(table.Row{
					project.Name,
					project.PublicKey,
					project.SecretKey,
					project.AuthWebhookURL,
					strings.Join(project.AuthWebhookMethods, ","),
					project.AuthWebhookMaxRetries,
					project.AuthWebhookMinWaitInterval,
					project.AuthWebhookMaxWaitInterval,
					project.AuthWebhookRequestTimeout,
					project.EventWebhookURL,
					strings.Join(project.EventWebhookEvents, ","),
					project.EventWebhookMaxRetries,
					project.EventWebhookMinWaitInterval,
					project.EventWebhookMaxWaitInterval,
					project.EventWebhookRequestTimeout,
					project.ClientDeactivateThreshold,
					project.SnapshotThreshold,
					project.SnapshotInterval,
					project.MaxSubscribersPerDocument,
					project.MaxAttachmentsPerDocument,
					project.RemoveOnDetach,
					units.HumanDuration(time.Now().UTC().Sub(project.CreatedAt)),
				})
			}
		} else {
			// Show only essential fields by default
			tw.AppendHeader(table.Row{
				"NAME",
				"PUBLIC KEY",
				"SECRET KEY",
				"CREATED AT",
			})
			for _, project := range projects {
				tw.AppendRow(table.Row{
					project.Name,
					project.PublicKey,
					project.SecretKey,
					units.HumanDuration(time.Now().UTC().Sub(project.CreatedAt)),
				})
			}
		}
		cmd.Println(tw.Render())
	case JSONOutput:
		jsonOutput, err := json.MarshalIndent(projects, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		cmd.Println(string(jsonOutput))
	case YamlOutput:
		yamlOutput, err := yaml.Marshal(projects)
		if err != nil {
			return fmt.Errorf("marshal YAML: %w", err)
		}
		cmd.Println(string(yamlOutput))
	default:
		return fmt.Errorf("unknown output format: %s", output)
	}

	return nil
}

func init() {
	SubCmd.AddCommand(newListCommand())
}
