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

// Package user provides the user command.
package user

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func deleteAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete-account",
		Short:   "Delete user account",
		PreRunE: config.Preload,
		RunE: func(_ *cobra.Command, args []string) error {
			fmt.Print("Enter Password: ")
			bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			password = string(bytePassword)
			fmt.Println()

			fmt.Println(
				"WARNING: This action is irreversible. Your account and all associated data will be permanently deleted.",
			)

			fmt.Print("Are you absolutely sure? Type 'DELETE' to confirm: ")
			var confirmation string
			if _, err := fmt.Scanln(&confirmation); err != nil {
				return fmt.Errorf("failed to read confirmation: %w", err)
			}

			if confirmation != "DELETE" {
				return fmt.Errorf("account deletion aborted")
			}

			conf, err := config.Load()
			if err != nil {
				return err
			}

			if rpcAddr == "" {
				rpcAddr = viper.GetString("rpcAddr")
			}

			if deleteAccountFromServer(conf, rpcAddr, insecure, username, password) == nil {
				fmt.Println("Your account has been successfully deleted.")
			}

			return nil
		},
	}
}

func deleteAccountFromServer(conf *config.Config, rpcAddr string, insecureFlag bool, username, password string) error {
	cli, err := admin.Dial(rpcAddr,
		admin.WithInsecure(insecureFlag),
		admin.WithToken(conf.Auths[rpcAddr].Token),
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

	delete(conf.Auths, rpcAddr)
	if conf.RPCAddr == rpcAddr {
		for addr := range conf.Auths {
			conf.RPCAddr = addr
			break
		}
	}

	if err := config.Save(conf); err != nil {
		return err
	}

	return nil
}

func init() {
	cmd := deleteAccountCmd()
	cmd.Flags().StringVarP(
		&username,
		"username",
		"u",
		"",
		"Username (required if password is set)",
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
	SubCmd.AddCommand(cmd)
}
