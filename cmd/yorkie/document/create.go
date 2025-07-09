/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
)

var initialRoot string

const defaultYSON = `{
  "str": "value1",
  "num": 42,
  "int": Int(42),
  "long": Long(64),
  "null": null,
  "bool": true,
  "bytes": BinData("AQID"),
  "date": Date("2025-01-02T15:04:05.058Z"),
  "counter": Counter(Int(10)),
  "text": Text([{"val":"Hello","attrs":{"color":"red"}},{"val":"World","attrs":{"color":"blue"}}]),
  "tree": Tree({"type":"p","attrs":{"align":"center"},"children":[{"type":"text","value":"Hello World"}]})
}
`

func newCreateDocumentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create [project name] [document key]",
		Short: "Create a new document",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("requires project name and document key")
			}
			return nil
		},
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := args[0]
			documentKey := args[1]

			rpcAddr := viper.GetString("rpcAddr")
			auth, err := config.LoadAuth(rpcAddr)
			if err != nil {
				return err
			}

			cli, err := admin.Dial(rpcAddr,
				admin.WithToken(auth.Token),
				admin.WithInsecure(auth.Insecure),
			)
			if err != nil {
				return err
			}
			defer func() {
				cli.Close()
			}()

			rootStr := initialRoot
			if rootStr == "" {
				rootStr = defaultYSON
			}

			var obj yson.Object
			if err := yson.Unmarshal(rootStr, &obj); err != nil {
				return fmt.Errorf("failed to parse YSON: %w", err)
			}

			ctx := context.Background()
			doc, err := cli.CreateDocument(ctx, projectName, documentKey, obj)
			if err != nil {
				return fmt.Errorf("failed to create document: %w", err)
			}

			cmd.Printf("Document created: %s (%s)\n", doc.Key, doc.ID)
			return nil
		},
	}
}

func init() {
	cmd := newCreateDocumentCmd()
	cmd.Flags().StringVar(
		&initialRoot,
		"initial-root",
		"",
		"(optional) YSON string representing the initial root of the document",
	)
	SubCmd.AddCommand(cmd)
}
