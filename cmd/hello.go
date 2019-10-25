package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hackerwins/rottie/client"
	"github.com/hackerwins/rottie/pkg/log"
)

var (
	flagBody string
)

func newHelloCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hello [options]",
		Short: "Tests client",
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := client.NewRPCClient()
			if err != nil {
				return err
			}
			defer func() {
				if err := cli.Close(); err != nil {
					log.Logger.Error(err)
				}
			}()

			if err := cli.Hello(flagBody); err != nil {
				return err
			}

			return nil
		},
	}
}

func init() {
	cmd := newHelloCmd()
	cmd.Flags().StringVarP(&flagBody, "body", "B", "world", "body")
	rootCmd.AddCommand(cmd)
}
