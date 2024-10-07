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
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func deleteAccountCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "delete-account",
		Short:   "Delete account",
		PreRunE: config.Preload,
		RunE: func(_ *cobra.Command, args []string) error {
			rpcAddr := viper.GetString("rpcAddr")
			auth, err := config.LoadAuth(rpcAddr)
			if err != nil {
				return err
			}

			if err := readPassword(); err != nil {
				return err
			}

			if confirmation, err := makeConfirmation(); !confirmation || err != nil {
				if err != nil {
					return err
				}
				return nil
			}

			conf, err := config.Load()
			if err != nil {
				return err
			}

			if rpcAddr == "" {
				rpcAddr = viper.GetString("rpcAddr")
			}

			if err := deleteAccount(conf, auth, rpcAddr, username, password); err != nil {
				fmt.Println("delete account: ", err)
			}

			return nil
		},
	}
}

func makeConfirmation() (bool, error) {
	fmt.Println("Warning: This action cannot be undone. Type 'DELETE' to confirm: ")
	var confirmation string
	if _, err := fmt.Scanln(&confirmation); err != nil {
		return false, fmt.Errorf("read confirmation from user: %w", err)
	}

	if confirmation != "DELETE" {
		return false, fmt.Errorf("account deletion aborted")
	}

	return true, nil
}

func deleteAccount(conf *config.Config, auth config.Auth, rpcAddr, username, password string) error {
	cli, err := admin.Dial(rpcAddr, admin.WithToken(auth.Token), admin.WithInsecure(auth.Insecure))
	if err != nil {
		return err
	}
	defer func() {
		cli.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := cli.DeleteAccount(ctx, username, password); err != nil {
		return fmt.Errorf("delete account: %w", err)
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
		"Username",
	)
	cmd.Flags().StringVarP(
		&password,
		"password",
		"p",
		"",
		"Password (optional)",
	)

	_ = cmd.MarkFlagRequired("username")
	rootCmd.AddCommand(cmd)
}
