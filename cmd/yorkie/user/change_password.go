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
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"

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
			password, newPassword, err := getPasswords()
			if err != nil {
				return err
			}

			if rpcAddr == "" {
				rpcAddr = viper.GetString("rpcAddr")
			}

			cli, err := admin.Dial(rpcAddr, admin.WithInsecure(insecure))
			if err != nil {
				return fmt.Errorf("failed to dial admin: %w", err)
			}
			defer func() {
				cli.Close()
			}()

			ctx := context.Background()
			if err := cli.ChangePassword(ctx, username, password, newPassword); err != nil {
				return err
			}

			if err := deleteAuthSession(rpcAddr); err != nil {
				return err
			}

			return nil
		},
	}
}

func getPasswords() (string, string, error) {
	fmt.Print("Enter Password: ")
	bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", "", fmt.Errorf("failed to read password: %w", err)
	}
	password := string(bytePassword)
	fmt.Println()

	fmt.Print("Enter New Password: ")
	bytePassword, err = term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", "", fmt.Errorf("failed to read password: %w", err)
	}
	newPassword := string(bytePassword)
	fmt.Println()

	return password, newPassword, nil
}

func deleteAuthSession(rpcAddr string) error {
	conf, err := config.Load()
	if err != nil {
		return err
	}

	delete(conf.Auths, rpcAddr)
	if err := config.Save(conf); err != nil {
		return err
	}

	return nil
}

func init() {
	cmd := changePasswordCmd()
	cmd.Flags().StringVarP(
		&username,
		"username",
		"u",
		"",
		"Username (required)",
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
	_ = cmd.MarkFlagRequired("username")
	SubCmd.AddCommand(cmd)
}
