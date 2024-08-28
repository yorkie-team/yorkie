/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	usernameForSignOut string
	passwordForSignOut string
)

func deleteAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete-account",
		Short:   "Delete user account",
		PreRunE: config.Preload,
		RunE: func(_ *cobra.Command, args []string) error {
			fmt.Println(
				"WARNING: This action is irreversible. Your account and all associated data will be permanently deleted.",
			)

			conf, err := config.Load()
			if err != nil {
				return err
			}

			fmt.Print("Are you absolutely sure? Type 'DELETE' to confirm: ")
			var confirmation string
			if _, err := fmt.Scanln(&confirmation); err != nil {
				return fmt.Errorf("failed to read confirmation: %w", err)
			}

			if confirmation != "DELETE" {
				return fmt.Errorf("account deletion aborted")
			}

			if deleteAccountFromServer(conf, usernameForSignOut, passwordForSignOut) == nil {
				fmt.Println("Your account has been successfully deleted.")
			}

			return nil
		},
	}
}

func deleteAccountFromServer(conf *config.Config, username, password string) error {
	cli, err := admin.Dial(conf.RPCAddr,
		admin.WithInsecure(false),
		admin.WithToken(conf.Auths[conf.RPCAddr].Token),
	)
	if err != nil {
		return err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := cli.DeleteAccount(ctx, username, password); err != nil {
		return err
	}

	delete(conf.Auths, conf.RPCAddr)
	if err := config.Save(conf); err != nil {
		return err
	}

	return nil
}

func init() {
	cmd := deleteAccountCmd()
	cmd.Flags().StringVarP(
		&usernameForSignOut,
		"username",
		"u",
		"",
		"Username (required if password is set)",
	)
	cmd.Flags().StringVarP(
		&passwordForSignOut,
		"password",
		"p",
		"",
		"Password (required if username is set)",
	)
	cmd.MarkFlagsRequiredTogether("username", "password")
	rootCmd.AddCommand(cmd)
}
