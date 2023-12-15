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

package main

import (
	"context"
	"errors"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

var (
	previousSeq int64
	pageSize    int32
	isForward   bool
)

func newHistoryCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "history [project name] [document key]",
		Short:   "Show the history of a document",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("project name and document key are required")
			}

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
			changes, err := cli.ListChangeSummaries(
				ctx,
				args[0],
				key.Key(args[1]),
				previousSeq,
				pageSize,
				isForward,
			)
			if err != nil {
				return err
			}

			tw := table.NewWriter()
			tw.Style().Options.DrawBorder = false
			tw.Style().Options.SeparateColumns = false
			tw.Style().Options.SeparateFooter = false
			tw.Style().Options.SeparateHeader = false
			tw.Style().Options.SeparateRows = false
			tw.AppendHeader(table.Row{
				"SEQ",
				"MESSAGE",
				"SNAPSHOT",
			})
			for _, change := range changes {
				tw.AppendRow(table.Row{
					change.ID.ServerSeq(),
					change.Message,
					change.Snapshot,
				})
			}
			cmd.Printf("%s\n", tw.Render())
			return nil
		},
	}
}

func init() {
	cmd := newHistoryCmd()
	cmd.Flags().Int64Var(
		&previousSeq,
		"previous-seq",
		0,
		"The previous sequence to start from",
	)
	cmd.Flags().Int32Var(
		&pageSize,
		"size",
		0,
		"The number of history sequences to output per page",
	)
	cmd.Flags().BoolVar(
		&isForward,
		"forward",
		false,
		"Whether to search forward or backward",
	)
	rootCmd.AddCommand(cmd)
}
