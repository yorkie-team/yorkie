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

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	username string
	password string
	rpcAddr  string
	insecure bool
)

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "login",
		Short:   "Log in to Yorkie server",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := admin.Dial(rpcAddr, admin.WithInsecure(insecure))
			if err != nil {
				return err
			}
			defer func() {
				cli.Close()
			}()

			ctx := context.Background()
			token, err := cli.LogIn(ctx, username, password)
			if err != nil {
				return err
			}

			conf, err := config.Load()
			if err != nil {
				return err
			}

			if conf.Auths == nil {
				conf.Auths = make(map[string]config.Auth)
			}
			conf.Auths[rpcAddr] = config.Auth{
				Token:    token,
				Insecure: insecure,
			}
			conf.RPCAddr = rpcAddr
			if err := config.Save(conf); err != nil {
				return err
			}

			return nil
		},
	}
}

func init() {
	cmd := newLoginCmd()
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
	cmd.Flags().StringVar(
		&rpcAddr,
		"rpc-addr",
		"localhost:8080",
		"Address of the rpc server",
	)
	cmd.Flags().BoolVar(
		&insecure,
		"insecure",
		false,
		"Skip the TLS connection of the client",
	)
	cmd.MarkFlagsRequiredTogether("username", "password")
	rootCmd.AddCommand(cmd)
}
