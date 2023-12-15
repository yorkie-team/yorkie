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

package document

import (
	"context"
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	flagForce bool
)

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "remove [project name] [document key]",
		Short:   "Remove documents in the project",
		Example: "yorkie document remove sample-project sample-document [options]",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("project name and document key are required")
			}
			projectName := args[0]
			documentKey := args[1]

			rpcAddr := viper.GetString("rpcAddr")
			auth, err := config.LoadAuth(rpcAddr)
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

			return cli.RemoveDocument(ctx, projectName, documentKey, flagForce)
		},
	}
}

func init() {
	cmd := newRemoveCommand()
	cmd.Flags().BoolVar(
		&flagForce,
		"force",
		false,
		"force remove document even if it is attached to clients",
	)
	SubCmd.AddCommand(cmd)
}
