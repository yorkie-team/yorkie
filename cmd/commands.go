package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "rottie [options]",
	Short: "Realtime database backend based on MongoDB, CRDT",
}

func Run() int {
	if err := rootCmd.Execute(); err != nil {
		return 1
	}

	return 0
}
