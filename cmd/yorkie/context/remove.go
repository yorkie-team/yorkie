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

package context

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove [rpcAddr]",
		Short:   "Remove the context for the specified rpcAddr",
		Args:    cobra.ExactArgs(1),
		PreRunE: config.ReadConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			conf, err := config.Load()
			if err != nil {
				return err
			}

			rpcAddr := args[0]

			// Check if auth exists for the rpcAddr
			if _, ok := conf.Auths[rpcAddr]; !ok {
				return fmt.Errorf("no context exists for the rpcAddr: %s", rpcAddr)
			}

			// Delete the context for the rpcAddr
			delete(conf.Auths, rpcAddr)

			err = config.Save(conf)
			if err != nil {
				return err
			}

			fmt.Printf("Context for rpcAddr: %s has been removed.\n", rpcAddr)
			return nil
		},
	}
}

func init() {
	SubCmd.AddCommand(newRemoveCmd())
}
