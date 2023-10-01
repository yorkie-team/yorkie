/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package context

import (
	"fmt"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Short:   "List all contexts from configuration",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			conf, err := config.Load()
			if err != nil {
				return err
			}

			tw := table.NewWriter()
			tw.Style().Options.DrawBorder = false
			tw.Style().Options.SeparateColumns = false
			tw.Style().Options.SeparateFooter = false
			tw.Style().Options.SeparateHeader = false
			tw.Style().Options.SeparateRows = false

			tw.AppendHeader(table.Row{"CURRENT", "RPC ADDR", "INSECURE", "TOKEN"})
			for rpcAddr, auth := range conf.Auths {
				current := ""
				if rpcAddr == viper.GetString("rpcAddr") {
					current = "*"
				}

				insecure := "false"
				if auth.Insecure {
					insecure = "true"
				}

				ellipsisToken := auth.Token
				if len(auth.Token) > 10 {
					ellipsisToken = auth.Token[:10] + "..." + auth.Token[len(auth.Token)-10:]
				}

				tw.AppendRow(table.Row{current, rpcAddr, insecure, ellipsisToken})
			}

			fmt.Println(tw.Render())

			return nil
		},
	}
}

func init() {
	SubCmd.AddCommand(newListCommand())
}
