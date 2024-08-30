/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package user

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	newPassword string
)

func changePasswordCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "change-password",
		Short:   "Change user password",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rpcAddr == "" {
				rpcAddr = viper.GetString("rpcAddr")
			}

			cli, err := admin.Dial(rpcAddr, admin.WithInsecure(insecure))
			if err != nil {
				return err
			}
			defer func() {
				cli.Close()
			}()

			ctx := context.Background()
			if err := cli.ChangePassword(ctx, username, password, newPassword); err != nil {
				return err
			}

			conf, err := config.Load()
			if err != nil {
				return err
			}

			delete(conf.Auths, rpcAddr)
			if err := config.Save(conf); err != nil {
				return err
			}

			return nil
		},
	}
}

func init() {
	cmd := changePasswordCmd()
	cmd.Flags().StringVarP(
		&username,
		"username",
		"u",
		"",
		"Username (required if password is set)",
	)
	cmd.Flags().StringVarP(
		&password,
		"password",
		"p",
		"",
		"Password (required if username is set)",
	)
	cmd.Flags().StringVarP(
		&newPassword,
		"new-password",
		"n",
		"",
		"New Password (required for change password)",
	)
	cmd.Flags().StringVar(
		&rpcAddr,
		"rpc-addr",
		"",
		"Address of the RPC server",
	)
	cmd.Flags().BoolVar(
		&insecure,
		"insecure",
		false,
		"Skip the TLS connection of the client",
	)
	cmd.MarkFlagsRequiredTogether("username", "password", "new-password")
	SubCmd.AddCommand(cmd)
}
