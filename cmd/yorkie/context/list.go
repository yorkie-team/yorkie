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
	"encoding/json"
	"fmt"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

type contextInfo struct {
	Current  string `json:"current" yaml:"current"`
	RPCAddr  string `json:"rpc_addr" yaml:"rpc_addr"`
	Insecure string `json:"insecure" yaml:"insecure"`
	Token    string `json:"token" yaml:"token"`
}

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

			contexts := make([]contextInfo, 0, len(conf.Auths))
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

				contexts = append(contexts, contextInfo{
					Current:  current,
					RPCAddr:  rpcAddr,
					Insecure: insecure,
					Token:    ellipsisToken,
				})
			}

			output := viper.GetString("output")
			if err := printContexts(cmd, output, contexts); err != nil {
				return err
			}

			return nil
		},
	}
}

func printContexts(cmd *cobra.Command, output string, contexts []contextInfo) error {
	switch output {
	case "":
		tw := table.NewWriter()
		tw.Style().Options.DrawBorder = false
		tw.Style().Options.SeparateColumns = false
		tw.Style().Options.SeparateFooter = false
		tw.Style().Options.SeparateHeader = false
		tw.Style().Options.SeparateRows = false

		tw.AppendHeader(table.Row{"CURRENT", "RPC ADDR", "INSECURE", "TOKEN"})
		for _, ctx := range contexts {
			tw.AppendRow(table.Row{ctx.Current, ctx.RPCAddr, ctx.Insecure, ctx.Token})
		}
		cmd.Println(tw.Render())
	case "json":
		marshalled, err := json.MarshalIndent(contexts, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		cmd.Println(string(marshalled))
	case "yaml":
		marshalled, err := yaml.Marshal(contexts)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML: %w", err)
		}
		cmd.Println(string(marshalled))
	default:
		return fmt.Errorf("unknown output format: %s", output)
	}

	return nil
}

func init() {
	SubCmd.AddCommand(newListCommand())
}
