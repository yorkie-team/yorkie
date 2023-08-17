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

package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	flagForce bool
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out from the Yorkie server",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf, err := config.Load()
			if err != nil {
				return err
			}
			if flagForce {
				return config.Delete()
			}
			if config.RPCAddr == "" {
				return errors.New("you must specify the server address to log out")
			}
			if _, ok := conf.Auths[config.RPCAddr]; !ok {
				return fmt.Errorf("you are not logged in to %s", config.RPCAddr)
			}
			if len(conf.Auths) <= 1 {
				return config.Delete()
			}
			delete(conf.Auths, config.RPCAddr)
			return config.Save(conf)
		},
	}
}

func init() {
	cmd := newLogoutCmd()
	cmd.Flags().BoolVar(
		&flagForce,
		"force",
		false,
		"force log out from all servers",
	)
	rootCmd.AddCommand(cmd)
}
