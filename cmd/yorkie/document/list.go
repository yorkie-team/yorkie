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
	"encoding/json"
	"errors"
	"fmt"
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

var (
	previousID string
	pageSize   int32
	isForward  bool
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "ls [project name]",
		Short:   "List all documents in the project",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("project is required")
			}
			projectName := args[0]

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
			documents, err := cli.ListDocuments(ctx, projectName, previousID, pageSize, isForward, true)
			if err != nil {
				return err
			}

			output := viper.GetString("output")
			if err := printDocuments(cmd, output, documents); err != nil {
				return err
			}

			return nil
		},
	}
}

func printDocuments(cmd *cobra.Command, output string, documents []*types.DocumentSummary) error {
	switch output {
	case "":
		tw := table.NewWriter()
		tw.Style().Options.DrawBorder = false
		tw.Style().Options.SeparateColumns = false
		tw.Style().Options.SeparateFooter = false
		tw.Style().Options.SeparateHeader = false
		tw.Style().Options.SeparateRows = false
		tw.AppendHeader(table.Row{
			"ID",
			"KEY",
			"CLIENTS",
			"CREATED AT",
			"ACCESSED AT",
			"UPDATED AT",
			"SNAPSHOT",
		})
		for _, document := range documents {
			tw.AppendRow(table.Row{
				document.ID,
				document.Key,
				document.AttachedClients,
				units.HumanDuration(time.Now().UTC().Sub(document.CreatedAt)),
				units.HumanDuration(time.Now().UTC().Sub(document.AccessedAt)),
				units.HumanDuration(time.Now().UTC().Sub(document.UpdatedAt)),
				document.Root,
			})
		}
		cmd.Printf("%s\n", tw.Render())
	case "json":
		jsonOutput, err := json.MarshalIndent(documents, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		cmd.Println(string(jsonOutput))
	case "yaml":
		yamlOutput, err := yaml.Marshal(documents)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML: %w", err)
		}
		cmd.Println(string(yamlOutput))
	default:
		return fmt.Errorf("unknown output format: %s", output)
	}

	return nil
}

func init() {
	cmd := newListCommand()
	cmd.Flags().StringVar(
		&previousID,
		"previous-id",
		"",
		"The previous document ID to start from",
	)
	cmd.Flags().Int32Var(
		&pageSize,
		"size",
		10,
		"The number of document to output per page",
	)
	cmd.Flags().BoolVar(
		&isForward,
		"forward",
		false,
		"Whether to search forward or backward",
	)
	SubCmd.AddCommand(cmd)
}
