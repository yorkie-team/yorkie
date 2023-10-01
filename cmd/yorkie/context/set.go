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
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newSetContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "set [RPCAddr]",
		Short:   "Set the current context to the given RPCAddr",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			conf, err := config.Load()
			if err != nil {
				return err
			}

			rpcAddr := viper.GetString("rpcAddr")
			conf.RPCAddr = rpcAddr

			if _, ok := conf.Auths[rpcAddr]; !ok {
				delete(conf.Auths, rpcAddr)
			}

			return config.Save(conf)
		},
	}
}

func init() {
	SubCmd.AddCommand(newSetContextCmd())
}
