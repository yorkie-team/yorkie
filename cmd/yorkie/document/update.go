package document

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

var updateRoot string

func newUpdateDocumentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [project name] [document key]",
		Short: "Update a document",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("requires project name and document key")
			}
			if updateRoot == "" {
				return errors.New("--root is required")
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

			cli, err := admin.Dial(
				rpcAddr,
				admin.WithToken(auth.Token),
				admin.WithInsecure(auth.Insecure),
			)
			if err != nil {
				return err
			}
			defer cli.Close()

			ctx := context.Background()
			doc, err := cli.UpdateDocument(ctx, projectName, key.Key(documentKey), updateRoot, "")
			if err != nil {
				return fmt.Errorf("failed to update document: %w", err)
			}

			cmd.Printf("Document updated: %s (%s)\n", doc.Key, doc.ID)
			return nil
		},
	}
}

func init() {
	cmd := newUpdateDocumentCmd()
	cmd.Flags().StringVar(
		&updateRoot,
		"root",
		"",
		"(required) YSON string representing the new root of the document",
	)
	_ = cmd.MarkFlagRequired("root")
	SubCmd.AddCommand(cmd)
}
