package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/hackerwins/yorkie/pkg/log"
	"github.com/hackerwins/yorkie/yorkie"
	"github.com/hackerwins/yorkie/yorkie/backend/mongo"
)

var (
	defaultConfig = &yorkie.Config{
		RPCPort: 9090,
		Mongo: &mongo.Config{
			ConnectionURI:        "mongodb://localhost:27017",
			ConnectionTimeoutSec: 5,
			PingTimeoutSec:       5,
			YorkieDatabase:       "yorkie-meta",
		},
	}
	gracefulTimeout = 10 * time.Second
)

var (
	flagRPCPort  int
	flagConfPath string
)

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent [options]",
		Short: "Starts yorkie agent.",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf := defaultConfig
			if flagConfPath != "" {
				parsed, err := yorkie.NewConfig(flagConfPath)
				if err != nil {
					return fmt.Errorf(
						"fail to create config: %s",
						flagConfPath,
					)
				}
				conf = parsed
			}

			r, err := yorkie.New(conf)
			if err != nil {
				return err
			}

			if err := r.Start(); err != nil {
				return err
			}

			if code := handleSignal(r); code != 0 {
				return fmt.Errorf("exit code: %d", code)
			}

			return nil
		},
	}
}

func handleSignal(r *yorkie.Yorkie) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var sig os.Signal
	select {
	case s := <-sigCh:
		sig = s
	case <-r.ShutdownCh():
		// yorkie is already shutdown
		return 0
	}

	graceful := false
	if sig == syscall.SIGINT || sig == syscall.SIGTERM {
		graceful = true
	}

	log.Logger.Infof("Caught signal: %s", sig.String())

	gracefulCh := make(chan struct{})
	go func() {
		if err := r.Shutdown(graceful); err != nil {
			log.Logger.Error(err)
			return
		}
		close(gracefulCh)
	}()

	select {
	case <-sigCh:
		return 1
	case <-time.After(gracefulTimeout):
		return 1
	case <-gracefulCh:
		return 0
	}
}

func init() {
	cmd := newAgentCmd()
	cmd.Flags().IntVarP(
		&flagRPCPort,
		"port",
		"P",
		defaultConfig.RPCPort,
		"rpc port",
	)
	cmd.Flags().StringVarP(
		&flagConfPath,
		"config",
		"C",
		"",
		"config path",
	)
	rootCmd.AddCommand(cmd)
}
