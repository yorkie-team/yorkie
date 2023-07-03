package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
)

var (
	flagForce bool
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out from the Yorkie server",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf, err := config.Load()
			if err != nil {
				return err
			}
			if flagForce {
				return config.Delete()
			}
			if config.RPCAddr == "" {
				return errors.New("you must specify the server address to log out")
			}
			if _, ok := conf.Auths[config.RPCAddr]; !ok {
				return fmt.Errorf("you are not logged in to %s", config.RPCAddr)
			}
			if len(conf.Auths) <= 1 {
				return config.Delete()
			}
			delete(conf.Auths, config.RPCAddr)
			return config.Save(conf)
		},
	}
}

func init() {
	cmd := newLogoutCmd()
	cmd.Flags().BoolVar(
		&flagForce,
		"force",
		false,
		"force log out from all servers",
	)
	rootCmd.AddCommand(cmd)
}
