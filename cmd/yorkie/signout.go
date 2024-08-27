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

var deleteAccountCmd = &cobra.Command{
	Use:   "delete-account",
	Short: "Delete your account",
	RunE:  runDeleteAccount,
}

func runDeleteAccount(cmd *cobra.Command, args []string) error {
	fmt.Println("WARNING: This action is irreversible. Your account and all associated data will be permanently deleted.")
	fmt.Print("To confirm, please type your username: ")

	var username string
	fmt.Scanln(&username)

	conf, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	fmt.Print("Please enter your password: ")
	var password string
	fmt.Scanln(&password)

	fmt.Print("Are you absolutely sure? Type 'DELETE' to confirm: ")
	var confirmation string
	fmt.Scanln(&confirmation)

	if confirmation != "DELETE" {
		return fmt.Errorf("account deletion aborted")
	}

	return deleteAccountFromServer(conf, username, password)
}

func deleteAccountFromServer(conf *config.Config, username, password string) error {
	cli, err := admin.Dial(conf.RPCAddr,
		admin.WithInsecure(false),
		admin.WithToken(conf.Auths[conf.RPCAddr].Token), // 토큰 기반 인증
	)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := cli.DeleteAccount(ctx, username, password); err != nil {
		return fmt.Errorf("failed to delete account: %w", err)
	}

	delete(conf.Auths, conf.RPCAddr)
	if err := config.Save(conf); err != nil {
		return fmt.Errorf("failed to update local config: %w", err)
	}

	fmt.Println("Your account has been successfully deleted.")
	return nil
}

func init() {
	rootCmd.AddCommand(deleteAccountCmd)
}
