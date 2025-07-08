package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

func newSignupCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "signup",
		Short:   "Sign up to the Yorkie server",
		PreRunE: config.Preload,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := readPassword(); err != nil {
				return err
			}

			cli, err := admin.Dial(rpcAddr, admin.WithInsecure(insecure))
			if err != nil {
				return err
			}
			defer cli.Close()

			ctx := context.Background()
			user, err := cli.SignUp(ctx, username, password)
			if err != nil {
				return fmt.Errorf("signup failed: %w", err)
			}

			cmd.Printf("Signed up as '%s' (ID: %s)\n", user.Username, user.ID)
			return nil
		},
	}
}

func init() {
	cmd := newSignupCmd()
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
	_ = cmd.MarkFlagRequired("username")
	rootCmd.AddCommand(cmd)
}
