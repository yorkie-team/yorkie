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
	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove [RPCAddr]",
		Short:   "Remove the context for the given RPCAddr",
		Args:    cobra.ExactArgs(1),
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return ErrRPCEmpty
			}

			conf, err := config.Load()
			if err != nil {
				return err
			}

			rpcAddr := args[0]
			if _, ok := conf.Auths[rpcAddr]; !ok {
				return ErrNotFoundAuth
			}

			delete(conf.Auths, rpcAddr)
			if conf.RPCAddr == rpcAddr {
				for addr := range conf.Auths {
					conf.RPCAddr = addr
					break
				}
			}

			return config.Save(conf)
		},
	}
}

func init() {
	SubCmd.AddCommand(newRemoveCmd())
}
