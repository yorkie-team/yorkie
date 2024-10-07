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

package main

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

func passwdCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "passwd",
		Short:   "Change password",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			rpcAddr := viper.GetString("rpcAddr")
			auth, err := config.LoadAuth(rpcAddr)
			if err != nil {
				return err
			}

			password, newPassword, err := readPasswords()
			if err != nil {
				return err
			}

			cli, err := admin.Dial(rpcAddr, admin.WithToken(auth.Token), admin.WithInsecure(auth.Insecure))
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

			if err := deleteAuthSession(rpcAddr); err != nil {
				return err
			}

			return nil
		},
	}
}

func readPasswords() (string, string, error) {
	fmt.Print("Enter Password: ")
	bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", "", fmt.Errorf("read password: %w", err)
	}
	password := string(bytePassword)
	fmt.Println()

	fmt.Print("Enter New Password: ")
	bytePassword, err = term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", "", fmt.Errorf("read new password: %w", err)
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
	cmd := passwdCmd()
	cmd.Flags().StringVarP(
		&username,
		"username",
		"u",
		"",
		"Username",
	)
	_ = cmd.MarkFlagRequired("username")
	rootCmd.AddCommand(cmd)
}
